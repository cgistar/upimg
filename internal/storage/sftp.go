package storage

import (
	"bytes"
	"context"
	"crypto/subtle"
	"fmt"
	"io"
	"net"
	"os"
	"path"
	"strconv"
	"strings"
	"sync"
	"time"

	pkgsftp "github.com/pkg/sftp"
	"golang.org/x/crypto/ssh"
	"upimg/internal/config"
	"upimg/internal/naming"
)

type SFTP struct {
	cfg      config.SFTPConfig
	rootPath string
}

const (
	sftpCommandKeepaliveInterval = 25 * time.Second
	sftpCommandKeepaliveTimeout  = 10 * time.Second
)

type SFTPHostKeyError struct {
	Host        string
	Fingerprint string
	Expected    string
}

func (e *SFTPHostKeyError) Error() string {
	if strings.TrimSpace(e.Expected) == "" {
		return fmt.Sprintf("sftp host key fingerprint is required for %s: got %s", e.Host, e.Fingerprint)
	}
	return fmt.Sprintf("sftp host key fingerprint mismatch for %s: got %s", e.Host, e.Fingerprint)
}

func NewSFTP(cfg config.SFTPConfig) (*SFTP, error) {
	if !cfg.Valid() {
		return nil, fmt.Errorf("sftp config is incomplete")
	}
	rootPath, err := cleanSFTPRootPath(cfg.RootPath)
	if err != nil {
		return nil, fmt.Errorf("invalid sftp rootPath: %w", err)
	}
	return &SFTP{cfg: cfg, rootPath: rootPath}, nil
}

func (s *SFTP) Type() string {
	return "sftp"
}

func (s *SFTP) UploadPath() string {
	return s.cfg.UploadPath
}

func (s *SFTP) Probe(ctx context.Context) error {
	return s.withClient(ctx, func(client *pkgsftp.Client) error {
		_, err := client.Stat(s.remoteObjectPath(""))
		return err
	})
}

func (s *SFTP) Put(ctx context.Context, key, fileName string, body io.Reader) (StoredObject, error) {
	key, err := naming.SafeRelative(key)
	if err != nil {
		return StoredObject{}, err
	}
	err = s.withClient(ctx, func(client *pkgsftp.Client) error {
		remotePath := s.remoteObjectPath(key)
		remoteDir := path.Dir(remotePath)
		if remoteDir != "." && remoteDir != "/" && remoteDir != "" {
			if err := client.MkdirAll(remoteDir); err != nil {
				return err
			}
		}

		tempPath := path.Join(remoteDir, fmt.Sprintf(".%s.%d.tmp", path.Base(remotePath), time.Now().UnixNano()))
		file, err := client.OpenFile(tempPath, os.O_CREATE|os.O_WRONLY|os.O_TRUNC)
		if err != nil {
			return err
		}
		if _, err := io.Copy(file, body); err != nil {
			_ = file.Close()
			_ = client.Remove(tempPath)
			return err
		}
		if err := file.Close(); err != nil {
			_ = client.Remove(tempPath)
			return err
		}
		if err := ctx.Err(); err != nil {
			_ = client.Remove(tempPath)
			return err
		}
		if err := s.renameReplace(client, tempPath, remotePath); err != nil {
			_ = client.Remove(tempPath)
			return err
		}
		return nil
	})
	if err != nil {
		return StoredObject{}, err
	}
	return StoredObject{FileName: fileName, URL: s.FileURL(key, ""), Type: s.Type()}, nil
}

func (s *SFTP) renameReplace(client *pkgsftp.Client, tempPath, remotePath string) error {
	if err := client.PosixRename(tempPath, remotePath); err == nil {
		return nil
	} else if !isUnsupportedSFTPExtension(err) {
		return err
	}
	_ = client.Remove(remotePath)
	return client.Rename(tempPath, remotePath)
}

func (s *SFTP) Delete(ctx context.Context, key string) error {
	key, err := naming.SafeRelative(key)
	if err != nil {
		return err
	}
	return s.withClient(ctx, func(client *pkgsftp.Client) error {
		return client.Remove(s.remoteObjectPath(key))
	})
}

func (s *SFTP) Rename(ctx context.Context, sourceKey, destinationKey string) error {
	sourceKey, err := naming.SafeRelative(sourceKey)
	if err != nil {
		return err
	}
	destinationKey, err = naming.SafeRelative(destinationKey)
	if err != nil {
		return err
	}
	return s.withClient(ctx, func(client *pkgsftp.Client) error {
		sourcePath := s.remoteObjectPath(sourceKey)
		destinationPath := s.remoteObjectPath(destinationKey)
		info, err := client.Stat(sourcePath)
		if err != nil {
			return err
		}
		if info.IsDir() {
			return fmt.Errorf("target is not a file")
		}
		if _, err := client.Stat(destinationPath); err == nil {
			return fmt.Errorf("destination already exists")
		} else if !os.IsNotExist(err) {
			return err
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		return client.Rename(sourcePath, destinationPath)
	})
}

func (s *SFTP) ExtractArchive(ctx context.Context, key string, overwrite bool) error {
	key, err := naming.SafeRelative(key)
	if err != nil {
		return err
	}
	format, err := archiveFormatForKey(key)
	if err != nil {
		return err
	}
	sftpClient, sshClient, err := s.connect(ctx)
	if err != nil {
		return err
	}
	defer sftpClient.Close()
	defer sshClient.Close()

	archivePath, err := s.sshObjectPath(ctx, sftpClient, key)
	if err != nil {
		return err
	}
	targetDir := path.Dir(archivePath)
	if targetDir == "." || targetDir == "" {
		targetDir = "."
	}
	if err := s.ensureArchiveCommands(ctx, sshClient, format); err != nil {
		return err
	}
	if format.listCommand != "" {
		entries, err := s.remoteOutput(ctx, sshClient, format.listCommand+" "+shellQuote(archivePath))
		if err != nil {
			return fmt.Errorf("读取压缩包目录失败：%w", err)
		}
		if err := validateArchiveEntries(entries); err != nil {
			return err
		}
	}
	command := format.extractCommand(archivePath, targetDir, overwrite)
	if _, err := s.remoteOutput(ctx, sshClient, command); err != nil {
		if isSSHExecUnsupported(err) {
			return fmt.Errorf("SFTP 服务器不支持 SSH 命令执行或当前账号无权限")
		}
		return fmt.Errorf("解压失败：%w", err)
	}
	return nil
}

func (s *SFTP) OpenCommandSession(ctx context.Context, dir string, stdout, stderr io.Writer) (CommandSession, error) {
	dir, err := normalizeListDir(dir)
	if err != nil {
		return nil, err
	}

	sftpClient, sshClient, err := s.connect(ctx)
	if err != nil {
		return nil, err
	}

	remoteDir, err := s.sshObjectPath(ctx, sftpClient, dir)
	if err != nil {
		_ = sftpClient.Close()
		_ = sshClient.Close()
		return nil, err
	}
	session, err := sshClient.NewSession()
	if err != nil {
		_ = sftpClient.Close()
		_ = sshClient.Close()
		return nil, err
	}

	session.Stdout = stdout
	session.Stderr = stderr
	stdin, err := session.StdinPipe()
	if err != nil {
		_ = session.Close()
		_ = sftpClient.Close()
		_ = sshClient.Close()
		return nil, err
	}
	modes := ssh.TerminalModes{
		ssh.ECHO:          0,
		ssh.TTY_OP_ISPEED: 14400,
		ssh.TTY_OP_OSPEED: 14400,
	}
	if err := session.RequestPty("xterm-256color", 40, 120, modes); err != nil && stderr != nil {
		_, _ = fmt.Fprintf(stderr, "PTY 不可用，已降级为普通 SSH exec：%v\n", err)
	}

	shellCommand := "cd " + shellQuote(remoteDir) + " && TERM=dumb PROMPT_COMMAND= PS1='$ ' exec ${SHELL:-/bin/sh} -i"
	if err := session.Start(shellCommand); err != nil {
		_ = session.Close()
		_ = sftpClient.Close()
		_ = sshClient.Close()
		return nil, err
	}

	keepaliveCtx, cancelKeepalive := context.WithCancel(ctx)
	commandSession := &sftpCommandSession{
		actualDir:       remoteDir,
		sftpClient:      sftpClient,
		sshClient:       sshClient,
		session:         session,
		stdin:           stdin,
		done:            make(chan error, 1),
		cancelKeepalive: cancelKeepalive,
	}
	go func() {
		commandSession.done <- session.Wait()
	}()
	go func() {
		<-ctx.Done()
		_ = commandSession.Close()
	}()
	go commandSession.keepAlive(keepaliveCtx)
	return commandSession, nil
}

func (s *SFTP) ensureArchiveCommands(ctx context.Context, client *ssh.Client, format archiveFormat) error {
	checked := make(map[string]bool)
	for _, command := range format.requiredCommands {
		if checked[command] {
			continue
		}
		checked[command] = true
		if _, err := s.remoteOutput(ctx, client, "command -v "+shellQuote(command)); err != nil {
			if isSSHExecUnsupported(err) {
				return fmt.Errorf("SFTP 服务器不支持 SSH 命令执行或当前账号无权限")
			}
			return fmt.Errorf("需要在 SFTP 服务器安装 %s 解压缩程序", command)
		}
	}
	return nil
}

type sftpCommandSession struct {
	actualDir       string
	sftpClient      *pkgsftp.Client
	sshClient       *ssh.Client
	session         *ssh.Session
	stdin           io.WriteCloser
	done            chan error
	closeOnce       sync.Once
	cancelKeepalive context.CancelFunc
}

func (s *sftpCommandSession) ActualDir() string {
	return s.actualDir
}

func (s *sftpCommandSession) WriteCommand(command string) error {
	command = strings.TrimRight(command, "\r\n")
	if strings.TrimSpace(command) == "" {
		return fmt.Errorf("command is required")
	}
	_, err := io.WriteString(s.stdin, command+"\n")
	return err
}

func (s *sftpCommandSession) Interrupt() error {
	_, writeErr := s.stdin.Write([]byte{3})
	signalErr := s.session.Signal(ssh.SIGINT)
	if writeErr != nil {
		return writeErr
	}
	return signalErr
}

func (s *sftpCommandSession) Close() error {
	var err error
	s.closeOnce.Do(func() {
		if s.cancelKeepalive != nil {
			s.cancelKeepalive()
		}
		_ = s.session.Signal(ssh.SIGHUP)
		_ = s.stdin.Close()
		if closeErr := s.session.Close(); closeErr != nil {
			err = closeErr
		}
		_ = s.sftpClient.Close()
		_ = s.sshClient.Close()
	})
	return err
}

func (s *sftpCommandSession) Wait() error {
	return <-s.done
}

func (s *sftpCommandSession) keepAlive(ctx context.Context) {
	ticker := time.NewTicker(sftpCommandKeepaliveInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			requestCtx, cancel := context.WithTimeout(ctx, sftpCommandKeepaliveTimeout)
			err := s.sendKeepalive(requestCtx)
			cancel()
			if err != nil {
				_ = s.Close()
				return
			}
		}
	}
}

func (s *sftpCommandSession) sendKeepalive(ctx context.Context) error {
	errCh := make(chan error, 1)
	go func() {
		_, _, err := s.sshClient.SendRequest("keepalive@openssh.com", true, nil)
		errCh <- err
	}()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case err := <-errCh:
		return err
	}
}

func (s *SFTP) sshObjectPath(ctx context.Context, client *pkgsftp.Client, key string) (string, error) {
	remotePath := s.remoteObjectPath(key)
	if path.IsAbs(remotePath) {
		return remotePath, nil
	}
	if err := ctx.Err(); err != nil {
		return "", err
	}
	wd, err := client.Getwd()
	if err != nil {
		return "", fmt.Errorf("resolve sftp working directory: %w", err)
	}
	return absoluteRemotePath(wd, remotePath), nil
}

func absoluteRemotePath(wd, remotePath string) string {
	remotePath = strings.TrimSpace(strings.ReplaceAll(remotePath, "\\", "/"))
	if path.IsAbs(remotePath) {
		return path.Clean(remotePath)
	}
	wd = strings.TrimSpace(strings.ReplaceAll(wd, "\\", "/"))
	if wd == "" || wd == "." {
		wd = "/"
	}
	return path.Clean(path.Join(wd, remotePath))
}

func (s *SFTP) CreateDir(ctx context.Context, key string) error {
	key, err := normalizeDirectoryKey(key)
	if err != nil {
		return err
	}
	return s.withClient(ctx, func(client *pkgsftp.Client) error {
		return client.MkdirAll(s.remoteObjectPath(key))
	})
}

func (s *SFTP) DeleteDir(ctx context.Context, key string) error {
	key, err := normalizeDirectoryKey(key)
	if err != nil {
		return err
	}
	return s.withClient(ctx, func(client *pkgsftp.Client) error {
		return s.deleteDir(ctx, client, s.remoteObjectPath(key))
	})
}

func (s *SFTP) OpenReader(ctx context.Context, key string) (io.ReadCloser, error) {
	key, err := naming.SafeRelative(key)
	if err != nil {
		return nil, err
	}
	client, sshClient, err := s.connect(ctx)
	if err != nil {
		return nil, err
	}
	file, err := client.Open(s.remoteObjectPath(key))
	if err != nil {
		_ = client.Close()
		_ = sshClient.Close()
		return nil, err
	}
	return &sftpReadCloser{ReadSeeker: file, cleanup: func() error {
		err := file.Close()
		if closeErr := client.Close(); err == nil {
			err = closeErr
		}
		if closeErr := sshClient.Close(); err == nil {
			err = closeErr
		}
		return err
	}}, nil
}

func (s *SFTP) FileURL(key, _ string) string {
	key = strings.TrimLeft(strings.ReplaceAll(key, "\\", "/"), "/")
	objectPath := key
	if s.rootPath != "" {
		objectPath = strings.TrimLeft(path.Join(s.rootPath, key), "/")
	}
	if prefix := strings.TrimSpace(s.cfg.URLPrefix); prefix != "" {
		return strings.TrimRight(prefix, "/") + "/" + key
	}
	return scpFilePath(strings.TrimSpace(s.cfg.Host), s.cfg.PortOrDefault(), "/"+objectPath)
}

func scpFilePath(host string, port int, remotePath string) string {
	if strings.Contains(host, ":") && !strings.HasPrefix(host, "[") {
		host = "[" + host + "]"
	}
	if port == 22 {
		return host + ":" + remotePath
	}
	return host + ":" + strconv.Itoa(port) + ":" + remotePath
}

func (s *SFTP) List(ctx context.Context, _ string, dir string) ([]Object, error) {
	dir, err := normalizeListDir(dir)
	if err != nil {
		return nil, err
	}

	var objects []Object
	err = s.withClient(ctx, func(client *pkgsftp.Client) error {
		entries, err := client.ReadDir(s.remoteObjectPath(dir))
		if os.IsNotExist(err) {
			return nil
		}
		if err != nil {
			return err
		}
		for _, entry := range entries {
			if err := ctx.Err(); err != nil {
				return err
			}
			key := path.Join(dir, entry.Name())
			object := Object{
				Path:    key,
				Size:    entry.Size(),
				ModTime: entry.ModTime(),
				Type:    s.Type(),
				IsDir:   entry.IsDir(),
			}
			if object.ModTime.IsZero() {
				object.ModTime = time.Unix(0, 0).UTC()
			}
			if object.IsDir {
				object.Path = strings.TrimRight(object.Path, "/") + "/"
				object.Size = 0
			} else {
				object.URL = s.FileURL(object.Path, "")
			}
			objects = append(objects, object)
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	sortObjects(objects)
	return objects, nil
}

func (s *SFTP) deleteDir(ctx context.Context, client *pkgsftp.Client, remotePath string) error {
	entries, err := client.ReadDir(remotePath)
	if err != nil {
		return err
	}
	for _, entry := range entries {
		if err := ctx.Err(); err != nil {
			return err
		}
		child := path.Join(remotePath, entry.Name())
		if entry.IsDir() {
			if err := s.deleteDir(ctx, client, child); err != nil {
				return err
			}
			continue
		}
		if err := client.Remove(child); err != nil {
			return err
		}
	}
	return client.RemoveDirectory(remotePath)
}

func (s *SFTP) withClient(ctx context.Context, fn func(*pkgsftp.Client) error) error {
	client, sshClient, err := s.connect(ctx)
	if err != nil {
		return err
	}
	defer sshClient.Close()
	defer client.Close()
	return fn(client)
}

func (s *SFTP) connect(ctx context.Context) (*pkgsftp.Client, *ssh.Client, error) {
	sshClient, err := s.connectSSH(ctx)
	if err != nil {
		return nil, nil, err
	}
	client, err := pkgsftp.NewClient(sshClient)
	if err != nil {
		_ = sshClient.Close()
		return nil, nil, err
	}
	return client, sshClient, nil
}

func (s *SFTP) connectSSH(ctx context.Context) (*ssh.Client, error) {
	auth, err := s.authMethods()
	if err != nil {
		return nil, err
	}
	sshConfig := &ssh.ClientConfig{
		User:            strings.TrimSpace(s.cfg.Username),
		Auth:            auth,
		HostKeyCallback: s.hostKeyCallback(),
		Timeout:         10 * time.Second,
	}
	address := net.JoinHostPort(strings.TrimSpace(s.cfg.Host), strconv.Itoa(s.cfg.PortOrDefault()))
	conn, err := (&net.Dialer{Timeout: 10 * time.Second}).DialContext(ctx, "tcp", address)
	if err != nil {
		return nil, err
	}
	sshConn, chans, reqs, err := ssh.NewClientConn(conn, address, sshConfig)
	if err != nil {
		_ = conn.Close()
		return nil, err
	}
	return ssh.NewClient(sshConn, chans, reqs), nil
}

func (s *SFTP) remoteOutput(ctx context.Context, client *ssh.Client, command string) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}
	session, err := client.NewSession()
	if err != nil {
		return "", err
	}
	defer session.Close()

	var output bytes.Buffer
	session.Stdout = &output
	session.Stderr = &output

	done := make(chan error, 1)
	go func() {
		done <- session.Run(command)
	}()
	select {
	case err := <-done:
		if err != nil {
			message := strings.TrimSpace(output.String())
			if message != "" {
				return "", fmt.Errorf("%s", message)
			}
			return "", err
		}
		return output.String(), nil
	case <-ctx.Done():
		_ = session.Close()
		return "", ctx.Err()
	}
}

func (s *SFTP) authMethods() ([]ssh.AuthMethod, error) {
	var auth []ssh.AuthMethod
	if strings.TrimSpace(s.cfg.Password) != "" {
		auth = append(auth, passwordAuthMethods(s.cfg.Password)...)
	}
	privateKey := strings.TrimSpace(s.cfg.PrivateKey)
	if privateKey != "" {
		var (
			signer ssh.Signer
			err    error
		)
		if strings.TrimSpace(s.cfg.Passphrase) != "" {
			signer, err = ssh.ParsePrivateKeyWithPassphrase([]byte(privateKey), []byte(s.cfg.Passphrase))
		} else {
			signer, err = ssh.ParsePrivateKey([]byte(privateKey))
		}
		if err != nil {
			return nil, fmt.Errorf("parse sftp privateKey: %w", err)
		}
		auth = append(auth, ssh.PublicKeys(signer))
	}
	if len(auth) == 0 {
		return nil, fmt.Errorf("sftp auth method is required")
	}
	return auth, nil
}

func passwordAuthMethods(password string) []ssh.AuthMethod {
	return []ssh.AuthMethod{
		ssh.Password(password),
		ssh.KeyboardInteractive(func(_ string, _ string, questions []string, _ []bool) ([]string, error) {
			answers := make([]string, len(questions))
			for i := range answers {
				answers[i] = password
			}
			return answers, nil
		}),
	}
}

func (s *SFTP) hostKeyCallback() ssh.HostKeyCallback {
	expected := strings.TrimSpace(s.cfg.HostKeyFingerprint)
	return func(hostname string, remote net.Addr, key ssh.PublicKey) error {
		actual := ssh.FingerprintSHA256(key)
		if expected == "" {
			return &SFTPHostKeyError{Host: hostname, Fingerprint: actual}
		}
		if subtle.ConstantTimeCompare([]byte(actual), []byte(expected)) == 1 {
			return nil
		}
		return &SFTPHostKeyError{Host: hostname, Fingerprint: actual, Expected: expected}
	}
}

func (s *SFTP) remoteObjectPath(key string) string {
	key = strings.Trim(strings.ReplaceAll(key, "\\", "/"), "/")
	if s.rootPath == "" {
		if key == "" {
			return "."
		}
		return key
	}
	if key == "" {
		return s.rootPath
	}
	return path.Join(s.rootPath, key)
}

func cleanSFTPRootPath(value string) (string, error) {
	value = strings.TrimSpace(strings.ReplaceAll(value, "\\", "/"))
	if value == "" {
		return "", nil
	}
	return naming.SafeRemotePath(value)
}

func isUnsupportedSFTPExtension(err error) bool {
	message := strings.ToLower(err.Error())
	return strings.Contains(message, "unimplemented") || strings.Contains(message, "unsupported")
}

type archiveFormat struct {
	requiredCommands []string
	listCommand      string
	extractCommand   func(archivePath, targetDir string, overwrite bool) string
}

func archiveFormatForKey(key string) (archiveFormat, error) {
	lower := strings.ToLower(key)
	formats := []struct {
		suffix string
		format archiveFormat
	}{
		{".tar.gz", tarArchiveFormat("tar -tzf", "tar -xzf", "gzip")},
		{".tgz", tarArchiveFormat("tar -tzf", "tar -xzf", "gzip")},
		{".tar.bz2", tarArchiveFormat("tar -tjf", "tar -xjf", "bzip2")},
		{".tbz2", tarArchiveFormat("tar -tjf", "tar -xjf", "bzip2")},
		{".tar.xz", tarArchiveFormat("tar -tJf", "tar -xJf", "xz")},
		{".txz", tarArchiveFormat("tar -tJf", "tar -xJf", "xz")},
		{".zip", zipArchiveFormat()},
		{".tar", tarArchiveFormat("tar -tf", "tar -xf")},
		{".gz", singleFileArchiveFormat("gzip")},
		{".bz2", singleFileArchiveFormat("bzip2")},
		{".xz", singleFileArchiveFormat("xz")},
	}
	for _, item := range formats {
		if strings.HasSuffix(lower, item.suffix) {
			return item.format, nil
		}
	}
	return archiveFormat{}, fmt.Errorf("unsupported archive format")
}

func tarArchiveFormat(listCommand, extractCommand string, helpers ...string) archiveFormat {
	required := append([]string{"tar"}, helpers...)
	return archiveFormat{
		requiredCommands: required,
		listCommand:      listCommand,
		extractCommand: func(archivePath, targetDir string, _ bool) string {
			return extractCommand + " " + shellQuote(archivePath) + " -C " + shellQuote(targetDir)
		},
	}
}

func zipArchiveFormat() archiveFormat {
	return archiveFormat{
		requiredCommands: []string{"unzip"},
		listCommand:      "unzip -Z1",
		extractCommand: func(archivePath, targetDir string, overwrite bool) string {
			flag := "-n"
			if overwrite {
				flag = "-oq"
			}
			return "unzip " + flag + " -- " + shellQuote(archivePath) + " -d " + shellQuote(targetDir)
		},
	}
}

func singleFileArchiveFormat(command string) archiveFormat {
	return archiveFormat{
		requiredCommands: []string{command},
		extractCommand: func(archivePath, _ string, overwrite bool) string {
			flag := "-dk"
			if overwrite {
				flag = "-dkf"
			}
			return command + " " + flag + " -- " + shellQuote(archivePath)
		},
	}
}

func validateArchiveEntries(output string) error {
	for _, entry := range strings.Split(output, "\n") {
		entry = strings.TrimSpace(entry)
		if entry == "" {
			continue
		}
		cleaned := strings.ReplaceAll(entry, "\\", "/")
		if strings.HasPrefix(cleaned, "/") {
			return fmt.Errorf("压缩包包含不安全的绝对路径：%s", entry)
		}
		for _, part := range strings.Split(cleaned, "/") {
			if part == ".." {
				return fmt.Errorf("压缩包包含不安全的上级路径：%s", entry)
			}
		}
	}
	return nil
}

func shellQuote(value string) string {
	return "'" + strings.ReplaceAll(value, "'", "'\\''") + "'"
}

func isSSHExecUnsupported(err error) bool {
	if err == nil {
		return false
	}
	message := strings.ToLower(err.Error())
	return strings.Contains(message, "ssh: session failed") ||
		strings.Contains(message, "exec request failed") ||
		strings.Contains(message, "administratively prohibited")
}

type sftpReadCloser struct {
	io.ReadSeeker
	cleanup func() error
}

func (s *sftpReadCloser) Close() error {
	return s.cleanup()
}
