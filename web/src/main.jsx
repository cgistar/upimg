import React, { useEffect, useMemo, useRef, useState } from 'react';
import { createPortal } from 'react-dom';
import { createRoot } from 'react-dom/client';
import { ArchiveRestore, Check, ChevronDown, Eraser, FilePenLine, FolderOpen, Link2, LogOut, Maximize2, Minimize2, OctagonX, Settings, SquarePen, Terminal, X } from 'lucide-react';
import './styles.css';

const emptyConfig = {
  host: '0.0.0.0',
  port: 17788,
  basePath: '',
  key: '',
  rename: '{filename}',
  filePath: '',
  urlPrefix: '',
  defaultTarget: '',
  s3: [],
  webdav: [],
  sftp: [],
};

const emptyS3 = {
  name: '',
  bucket: '',
  region: '',
  accessKeyID: '',
  secretAccessKey: '',
  endpoint: '',
  urlPrefix: '',
  uploadPath: '',
};

const emptyWebDAV = {
  name: '',
  endpoint: '',
  username: '',
  password: '',
  rootPath: '',
  urlPrefix: '',
  uploadPath: '',
};

const emptySFTP = {
  name: '',
  host: '',
  port: 22,
  username: '',
  password: '',
  privateKey: '',
  passphrase: '',
  hostKeyFingerprint: '',
  rootPath: '',
  urlPrefix: '',
  uploadPath: '',
};

const fieldLabels = {
  key: '访问密钥',
  rename: '命名规则',
  host: '主机',
  port: '端口',
  basePath: '访问前缀',
  filePath: '本地目录',
  urlPrefix: 'URL 链接',
  name: '配置名称',
  bucket: '存储桶',
  region: '区域',
  accessKeyID: 'Access Key ID',
  secretAccessKey: 'Secret Access Key',
  endpoint: '服务地址',
  uploadPath: '上传目录',
  username: '用户名',
  password: '密码',
  rootPath: '远程根目录',
  privateKey: '私钥内容',
  passphrase: '私钥口令',
};

async function api(path, options = {}) {
  const headers = options.body instanceof FormData ? {} : { 'Content-Type': 'application/json' };
  const response = await fetch(`${adminAPIBase()}${path}`, {
    credentials: 'same-origin',
    ...options,
    headers: { ...headers, ...(options.headers || {}) },
  });
  const data = await response.json().catch(() => ({ success: false, message: 'invalid server response' }));
  if (!response.ok || data.success === false) {
    const err = new Error(data.message || `request failed: ${response.status}`);
    err.data = data;
    throw err;
  }
  return data;
}

async function apiRaw(path, options = {}) {
  const response = await fetch(`${adminAPIBase()}${path}`, {
    credentials: 'same-origin',
    ...options,
  });
  if (!response.ok) {
    const data = await response.json().catch(() => null);
    throw new Error(data?.message || `request failed: ${response.status}`);
  }
  return response;
}

function uploadFileWithProgress(file, target, dir, onProgress) {
  return new Promise((resolve, reject) => {
    const params = new URLSearchParams({ target, path: dir || '' });
    const xhr = new XMLHttpRequest();
    xhr.open('POST', `${adminAPIBase()}/storage/upload?${params}`);
    xhr.withCredentials = true;
    xhr.upload.onprogress = (event) => {
      if (!event.lengthComputable) return;
      onProgress(Math.round((event.loaded / event.total) * 100));
    };
    xhr.onload = () => {
      const data = parseJSON(xhr.responseText);
      if (xhr.status < 200 || xhr.status >= 300 || data?.success === false) {
        reject(new Error(data?.message || `request failed: ${xhr.status}`));
        return;
      }
      resolve(data);
    };
    xhr.onerror = () => reject(new Error('upload failed'));
    xhr.onabort = () => reject(new Error('upload aborted'));

    const form = new FormData();
    form.append('file', file);
    xhr.send(form);
  });
}

function parseJSON(value) {
  try {
    return JSON.parse(value);
  } catch {
    return null;
  }
}

function adminAPIBase() {
  return `${adminBasePath()}/api/admin`;
}

function adminStorageDownloadURL(target, objectPath) {
  const params = new URLSearchParams({ target, path: objectPath });
  return `${adminAPIBase()}/storage/download?${params}`;
}

function adminSFTPTerminalURL(target) {
  const params = new URLSearchParams({ target });
  const protocol = window.location.protocol === 'https:' ? 'wss:' : 'ws:';
  return `${protocol}//${window.location.host}${adminAPIBase()}/sftp/terminal?${params}`;
}

function adminBasePath() {
  const injected = window.__UPIMG_ADMIN__?.basePath;
  if (typeof injected === 'string') return normalizeBasePath(injected);

  const adminIndex = window.location.pathname.lastIndexOf('/admin');
  return normalizeBasePath(adminIndex > 0 ? window.location.pathname.slice(0, adminIndex) : '');
}

function normalizeBasePath(value) {
  const normalized = String(value || '').trim().replace(/\/+$/, '');
  if (!normalized || normalized === '/') return '';
  return normalized.startsWith('/') ? normalized : `/${normalized}`;
}

function App() {
  const [session, setSession] = useState(null);
  const [configState, setConfigState] = useState(null);
  const [selectedTarget, setSelectedTarget] = useState('local');
  const [configTarget, setConfigTarget] = useState('local');
  const [path, setPath] = useState('');
  const [objects, setObjects] = useState([]);
  const [view, setView] = useState('files');
  const [message, setMessage] = useState('');
  const [warning, setWarning] = useState('');
  const [error, setError] = useState('');
  const [confirmState, setConfirmState] = useState(null);
  const [deleting, setDeleting] = useState(null);
  const [extracting, setExtracting] = useState(null);
  const [uploadDialog, setUploadDialog] = useState(null);
  const [virtualFolders, setVirtualFolders] = useState([]);
  const [busy, setBusy] = useState(false);
  const listRequestRef = useRef(0);
  const authenticated = session?.authenticated;

  useEffect(() => {
    api('/session')
      .then(setSession)
      .catch((err) => setError(err.message));
  }, []);

  useEffect(() => {
    if (!authenticated) return;
    loadConfig().catch((err) => setError(err.message));
  }, [authenticated]);

  useEffect(() => {
    if (!authenticated) return;
    loadObjects(selectedTarget, path);
  }, [authenticated, selectedTarget, path]);

  useEffect(() => {
    if (!message) return undefined;
    const timer = window.setTimeout(() => setMessage(''), 2600);
    return () => window.clearTimeout(timer);
  }, [message]);

  useEffect(() => {
    if (!warning) return undefined;
    const timer = window.setTimeout(() => setWarning(''), 3600);
    return () => window.clearTimeout(timer);
  }, [warning]);

  useEffect(() => {
    if (!error) return undefined;
    const timer = window.setTimeout(() => setError(''), 4200);
    return () => window.clearTimeout(timer);
  }, [error]);

  const visibleObjects = useMemo(
    () => mergeVirtualFolders(objects, virtualFolders, selectedTarget, path),
    [objects, virtualFolders, selectedTarget, path],
  );

  async function loadConfig() {
    const data = await api('/config');
    setConfigState(data);
    const defaultFileTarget = selectedTargetID(data.targets);
    if (selectedTarget !== defaultFileTarget) {
      setSelectedTarget(defaultFileTarget);
      setPath('');
    }
    if (!targetOptions(data.config).some((target) => target.id === configTarget)) {
      setConfigTarget('local');
    }
  }

  async function loadObjects(target, nextPath) {
    const requestId = listRequestRef.current + 1;
    listRequestRef.current = requestId;
    setBusy(true);
    try {
      const params = new URLSearchParams({ target, path: nextPath || '' });
      const data = await api(`/storage/list?${params}`);
      if (requestId !== listRequestRef.current) return;
      setObjects(data.result || []);
      setError('');
    } catch (err) {
      if (requestId !== listRequestRef.current) return;
      setObjects([]);
      setError(err.message);
    } finally {
      if (requestId === listRequestRef.current) setBusy(false);
    }
  }

  async function login(key) {
    try {
      await api('/login', { method: 'POST', body: JSON.stringify({ key }) });
      const data = await api('/session');
      setSession(data);
      setError('');
    } catch (err) {
      setError(err.message);
    }
  }

  async function logout() {
    try {
      await api('/logout', { method: 'POST', body: '{}' });
      setSession({ authenticated: false });
      setConfigState(null);
      setError('');
    } catch (err) {
      setError(err.message);
    }
  }

  async function uploadFiles(files, target, uploadPath) {
    const fileList = Array.from(files || []);
    if (fileList.length === 0) return;

    const items = fileList.map((file, index) => ({
      id: `${Date.now()}-${index}-${file.name}`,
      name: file.name,
      size: file.size,
      progress: 0,
      status: 'pending',
      message: '',
    }));
    setUploadDialog({ items, finished: false });

    let failed = 0;
    for (const [index, item] of items.entries()) {
      const file = fileList[index];
      setUploadDialogItem(item.id, { status: 'uploading', progress: 0, message: '上传中' });
      try {
        await uploadFileWithProgress(file, target, uploadPath, (progress) => {
          setUploadDialogItem(item.id, { progress });
        });
        setUploadDialogItem(item.id, { status: 'done', progress: 100, message: '完成' });
      } catch (err) {
        failed += 1;
        setUploadDialogItem(item.id, { status: 'failed', progress: 100, message: err.message });
      }
    }

    await loadObjects(target, uploadPath);
    setUploadDialog((current) => (current ? { ...current, finished: true } : current));
    if (failed > 0) {
      setError(`${failed} 个文件上传失败`);
      return;
    }
    setMessage(`已上传 ${fileList.length} 个文件`);
  }

  function setUploadDialogItem(id, patch) {
    setUploadDialog((current) => {
      if (!current) return current;
      return {
        ...current,
        items: current.items.map((item) => (item.id === id ? { ...item, ...patch } : item)),
      };
    });
  }

  const statusToast = (message || warning || error) ? <Toast type={error ? 'bad' : warning ? 'warn' : 'ok'} message={error || warning || message} /> : null;

  if (!session) {
    return <><Shell message="正在连接服务..." />{statusToast}</>;
  }
  if (!authenticated) {
    return <><Login onLogin={login} />{statusToast}</>;
  }
  if (!configState) {
    return <><Shell message="正在加载配置..." />{statusToast}</>;
  }

  return (
    <main className="app-shell">
      <header className="hero">
        <h1>upimg admin</h1>
        <div className="hero-actions">
          {view === 'files' && (
            <label className="hero-target">
              <span>文件服务</span>
              <select value={selectedTarget} onChange={(event) => {
                setSelectedTarget(event.target.value);
                setPath('');
              }}>
                {configState.targets.map((target) => (
                  <option key={target.id} value={target.id}>{target.name} · {target.type}</option>
                ))}
              </select>
            </label>
          )}
          <button
            className="icon-button"
            aria-label={view === 'config' ? '返回文件' : '配置'}
            title={view === 'config' ? '返回文件' : '配置'}
            onClick={() => setView(view === 'config' ? 'files' : 'config')}
          >
            {view === 'config' ? <FolderOpen size={18} strokeWidth={2.2} /> : <Settings size={18} strokeWidth={2.2} />}
          </button>
          <button className="icon-button" aria-label="退出" title="退出" onClick={logout}>
            <LogOut size={18} strokeWidth={2.2} />
          </button>
        </div>
      </header>

      {statusToast}
      {confirmState && (
        <ConfirmDialog
          title={confirmState.title}
          message={confirmState.message}
          confirmText={confirmState.confirmText}
          onCancel={() => setConfirmState(null)}
          onConfirm={async () => {
            const action = confirmState.action;
            setConfirmState(null);
            try {
              await action();
            } catch (err) {
              setError(err.message);
            }
          }}
        />
      )}
      {deleting && <DeleteOverlay message={deleting.message} />}
      {extracting && <ExtractOverlay item={extracting.item} path={path} />}
      {uploadDialog && (
        <UploadDialog
          upload={uploadDialog}
          onClose={() => {
            if (uploadDialog.finished) setUploadDialog(null);
          }}
        />
      )}
      {!session.keyConfigured && <div className="notice warn">当前未配置 key，管理界面处于免登录状态。</div>}
      {(configState.runtime.keyOverridden || configState.runtime.portOverridden || configState.runtime.basePathOverridden || configState.runtime.filePathOverridden) && (
        <div className="notice warn">检测到环境变量覆盖，保存 config.json 不会覆盖对应运行时值。</div>
      )}

      {view === 'files' ? (
        <FilePanel
          selectedTarget={selectedTarget}
          path={path}
          setPath={setPath}
          objects={visibleObjects}
          busy={busy}
          onRefresh={() => loadObjects(selectedTarget, path)}
          onCreateFolder={async (folderPath) => {
            if (selectedTarget.startsWith('s3:')) {
              setVirtualFolders((current) => addVirtualFolder(current, selectedTarget, folderPath));
              setMessage('文件夹已创建；上传文件后会在 S3 中生效');
              return;
            }
            const params = new URLSearchParams({ target: selectedTarget });
            await api(`/storage/folder?${params}`, { method: 'POST', body: JSON.stringify({ path: folderPath }) });
            setMessage('文件夹已创建');
            await loadObjects(selectedTarget, path);
          }}
          onUpload={async (files) => {
            await uploadFiles(files, selectedTarget, path);
          }}
          onBatchDelete={async (items) => {
            const virtualPaths = items.filter((item) => item.virtual).map((item) => item.path);
            const realPaths = items.filter((item) => !item.virtual).map((item) => item.path);
            if (virtualPaths.length > 0) {
              setVirtualFolders((current) => removeVirtualFolders(current, selectedTarget, virtualPaths));
            }
            if (realPaths.length === 0) {
              setMessage(`已删除 ${items.length} 项`);
              return;
            }
            const params = new URLSearchParams({ target: selectedTarget });
            setDeleting({ message: '正在删除所选项目...' });
            try {
              await api(`/storage/objects?${params}`, { method: 'DELETE', body: JSON.stringify({ paths: realPaths }) });
              setMessage(`已删除 ${items.length} 项`);
            } finally {
              await loadObjects(selectedTarget, path);
              setDeleting(null);
            }
          }}
          onRename={async (sourcePath, name) => {
            const params = new URLSearchParams({ target: selectedTarget });
            try {
              await api(`/storage/object/rename?${params}`, { method: 'PUT', body: JSON.stringify({ path: sourcePath, name }) });
            } catch (err) {
              if (!err.data?.partial) throw err;
              setWarning(err.message);
              await loadObjects(selectedTarget, path);
              return;
            }
            setMessage('文件名已更新');
            await loadObjects(selectedTarget, path);
          }}
          onExtract={async (item) => {
            const params = new URLSearchParams({ target: selectedTarget });
            setExtracting({ item });
            try {
              await api(`/storage/object/extract?${params}`, {
                method: 'POST',
                body: JSON.stringify({ path: item.path, overwrite: true }),
              });
              setMessage('解压完成');
            } finally {
              await loadObjects(selectedTarget, path);
              setExtracting(null);
            }
          }}
          onMessage={setMessage}
          onError={setError}
          onConfirm={setConfirmState}
        />
      ) : (
        <ConfigPanel
          configState={configState}
          selectedTarget={configTarget}
          setSelectedTarget={setConfigTarget}
          onSaved={(data) => {
            setConfigState(data);
            if (!data.targets.some((target) => target.id === selectedTarget)) {
              setSelectedTarget(selectedTargetID(data.targets));
              setPath('');
            }
            if (!targetOptions(data.config).some((target) => target.id === configTarget)) {
              setConfigTarget('local');
            }
            setMessage('配置已保存并热更新；host/port 如有变更需重启服务。');
          }}
          onMessage={setMessage}
          onWarn={setWarning}
          onError={setError}
        />
      )}
    </main>
  );
}

function Shell({ message }) {
  return <main className="center-card"><p>{message}</p></main>;
}

function Login({ onLogin }) {
  const [key, setKey] = useState('');
  return (
    <main className="login-page">
      <form className="login-card" onSubmit={(event) => {
        event.preventDefault();
        onLogin(key);
      }}>
        <h1>登录</h1>
        <input autoFocus type="password" value={key} onChange={(event) => setKey(event.target.value)} placeholder="请输入密码" />
        <button type="submit">登录</button>
      </form>
    </main>
  );
}

function Toast({ type, message }) {
  return (
    <div className={`toast ${type}`} role={type === 'bad' ? 'alert' : 'status'} aria-live={type === 'bad' ? 'assertive' : 'polite'}>
      {message}
    </div>
  );
}

function FilePanel({ selectedTarget, path, setPath, objects, busy, onRefresh, onCreateFolder, onUpload, onBatchDelete, onRename, onExtract, onMessage, onError, onConfirm }) {
  const [browserMode, setBrowserMode] = useState('list');
  const [preview, setPreview] = useState(null);
  const [folderDialog, setFolderDialog] = useState(false);
  const [dragActive, setDragActive] = useState(false);
  const [pathInput, setPathInput] = useState(path);
  const [pathHistory, setPathHistory] = useState([]);
  const [pathHistoryOpen, setPathHistoryOpen] = useState(false);
  const [selectedPaths, setSelectedPaths] = useState([]);
  const [renameState, setRenameState] = useState(null);
  const [commandDialog, setCommandDialog] = useState(false);
  const crumbs = path ? path.split('/').filter(Boolean) : [];
  const parentPath = crumbs.slice(0, -1).join('/');
  const entries = [
    { path: '.', displayName: '.', isDir: true, special: 'current' },
    ...(path ? [{ path: '..', displayName: '..', isDir: true, special: 'parent' }] : []),
    ...objects.map((item) => ({ ...item, displayName: basename(item.path) })),
  ];
  const selectableEntries = entries.filter((item) => !item.special);
  const selectedSet = new Set(selectedPaths);
  const allSelected = selectableEntries.length > 0 && selectableEntries.every((item) => selectedSet.has(item.path));
  const selectedItems = selectableEntries.filter((item) => selectedSet.has(item.path));
  const selectedDownloadableItems = selectedItems.filter((item) => !item.isDir && !item.virtual);
  const isSFTPTarget = selectedTarget.startsWith('sftp:');

  useEffect(() => {
    setSelectedPaths([]);
    setRenameState(null);
  }, [selectedTarget, path, objects]);

  useEffect(() => {
    setPathInput(path);
    setPathHistoryOpen(false);
  }, [path]);

  function changePath(nextPath) {
    const normalized = normalizeObjectPath(nextPath);
    if (normalized === path) return;
    setPathHistory((current) => addPathHistory(current, path));
    setPath(normalized);
  }

  function commitPathInput() {
    changePath(pathInput);
  }

  function selectPathHistory(event, historyPath) {
    event.preventDefault();
    setPathInput(historyPath);
    setPathHistoryOpen(false);
    changePath(historyPath);
  }

  function canRename(item) {
    return !item.special && !item.isDir;
  }

  function canCopyURL(item) {
    return !item.special && Boolean(item.url);
  }

  function canExtract(item) {
    return selectedTarget.startsWith('sftp:') && !item.special && !item.isDir && isArchiveFile(item);
  }

  function beginRename(event, item) {
    event.stopPropagation();
    if (!canRename(item)) return;
    setRenameState({ path: item.path, name: item.displayName, saving: false });
  }

  function confirmExtract(event, item) {
    event.stopPropagation();
    if (!canExtract(item)) return;
    onConfirm({
      title: '解压缩',
      message: `确认在当前目录解压缩 ${item.displayName}？同名文件将被覆盖。`,
      confirmText: '解压缩',
      action: () => onExtract(item),
    });
  }

  function cancelRename(event) {
    event?.stopPropagation();
    setRenameState(null);
  }

  async function copyURL(event, item) {
    event.stopPropagation();
    if (!canCopyURL(item)) return;
    try {
      await navigator.clipboard.writeText(item.url);
      onMessage('URL 已复制');
    } catch (err) {
      onError(`复制失败：${err.message}`);
    }
  }

  async function submitRename(event) {
    event.preventDefault();
    event.stopPropagation();
    if (!renameState || renameState.saving) return;
    const nextName = renameState.name.trim();
    const errorMessage = validateFileName(nextName);
    if (errorMessage) {
      onError(errorMessage);
      return;
    }
    setRenameState((current) => (current ? { ...current, saving: true } : current));
    try {
      await onRename(renameState.path, nextName);
      setRenameState(null);
    } catch (err) {
      setRenameState((current) => (current ? { ...current, saving: false } : current));
      onError(err.message);
    }
  }

  function renderEntryName(item, mode) {
    const editing = renameState?.path === item.path;
    if (editing) {
      return (
        <form className={`rename-form ${mode}`} onSubmit={submitRename} onClick={(event) => event.stopPropagation()}>
          <input
            autoFocus
            value={renameState.name}
            disabled={renameState.saving}
            onChange={(event) => setRenameState((current) => (current ? { ...current, name: event.target.value } : current))}
            onKeyDown={(event) => {
              if (event.key === 'Escape') cancelRename(event);
            }}
          />
          <button className="tiny-icon" type="submit" disabled={renameState.saving} aria-label="保存文件名" title="保存文件名">
            <Check size={12} strokeWidth={2.4} />
          </button>
          <button className="tiny-icon" type="button" disabled={renameState.saving} onClick={cancelRename} aria-label="取消重命名" title="取消重命名">
            <X size={12} strokeWidth={2.4} />
          </button>
        </form>
      );
    }
    return (
      <>
        <button className={mode === 'thumb' ? 'thumb-name' : 'name-button'} onClick={() => openEntry(item)}>
          <span>{item.displayName}</span>
        </button>
        {(canRename(item) || canCopyURL(item) || canExtract(item)) && (
          <span className="file-inline-actions">
            {canRename(item) && (
              <button className="tiny-icon" type="button" onClick={(event) => beginRename(event, item)} aria-label="重命名文件" title="重命名文件">
                <FilePenLine size={11} strokeWidth={2.3} />
              </button>
            )}
            {canCopyURL(item) && (
              <button className="tiny-icon" type="button" onClick={(event) => copyURL(event, item)} aria-label="复制 URL" title="复制 URL">
                <Link2 size={11} strokeWidth={2.3} />
              </button>
            )}
            {canExtract(item) && (
              <button className="tiny-icon" type="button" onClick={(event) => confirmExtract(event, item)} aria-label="解压缩到当前目录" title="解压缩到当前目录">
                <ArchiveRestore size={11} strokeWidth={2.3} />
              </button>
            )}
          </span>
        )}
      </>
    );
  }

  async function openEntry(item) {
    if (item.special === 'current') return;
    if (item.special === 'parent') {
      changePath(parentPath);
      return;
    }
    if (item.isDir) {
      changePath(item.path.replace(/\/$/, ''));
      return;
    }
    if (!item.url) return;
    if (isImage(item)) {
      setPreview({ type: 'image', title: item.displayName, url: item.url });
      return;
    }
    if (isTextFile(item)) {
      setPreview({ type: 'text', title: item.displayName, loading: true });
      try {
        const params = new URLSearchParams({ target: selectedTarget, path: item.path });
        const response = await apiRaw(`/storage/preview?${params}`);
        const text = await response.text();
        setPreview({ type: 'text', title: item.displayName, text });
      } catch (err) {
        setPreview(null);
        onError(`预览失败：${err.message}`);
      }
      return;
    }
    window.open(item.url, '_blank', 'noopener,noreferrer');
  }

  function togglePath(pathValue) {
    setSelectedPaths((current) => (
      current.includes(pathValue) ? current.filter((item) => item !== pathValue) : [...current, pathValue]
    ));
  }

  function toggleAll() {
    setSelectedPaths(allSelected ? [] : selectableEntries.map((item) => item.path));
  }

  function confirmBatchDelete() {
    if (selectedPaths.length === 0) return;
    onConfirm({
      title: '删除',
      message: `确认删除已选择的 ${selectedPaths.length} 项？其中的目录会递归删除，此操作不可撤销。`,
      confirmText: '删除',
      action: () => onBatchDelete(selectedItems),
    });
  }

  function downloadSelected() {
    if (selectedDownloadableItems.length === 0) return;
    for (const item of selectedDownloadableItems) {
      triggerDownload(adminStorageDownloadURL(selectedTarget, item.path), basename(item.path));
    }
    const skipped = selectedItems.length - selectedDownloadableItems.length;
    onMessage(skipped > 0
      ? `开始下载 ${selectedDownloadableItems.length} 个文件，已跳过 ${skipped} 个目录`
      : `开始下载 ${selectedDownloadableItems.length} 个文件`);
  }

  function handleDragEnter(event) {
    if (!event.dataTransfer?.types?.includes('Files')) return;
    event.preventDefault();
    setDragActive(true);
  }

  function handleDragOver(event) {
    if (!event.dataTransfer?.types?.includes('Files')) return;
    event.preventDefault();
    event.dataTransfer.dropEffect = 'copy';
    setDragActive(true);
  }

  function handleDragLeave(event) {
    if (!event.currentTarget.contains(event.relatedTarget)) {
      setDragActive(false);
    }
  }

  async function handleDrop(event) {
    event.preventDefault();
    setDragActive(false);
    const files = Array.from(event.dataTransfer?.files || []).filter((file) => file.size >= 0);
    if (files.length === 0) return;
    try {
      await onUpload(files);
    } catch (err) {
      onError(err.message);
    }
  }

  return (
    <section className="panel file-panel">
      <div
        className={dragActive ? 'nas-browser drag-active' : 'nas-browser'}
        onDragEnter={handleDragEnter}
        onDragOver={handleDragOver}
        onDragLeave={handleDragLeave}
        onDrop={handleDrop}
      >
        <div className="nas-toolbar">
          <div className="nas-actions">
            <div className="mode-toggle" aria-label="文件浏览模式">
              <button className={browserMode === 'list' ? 'active' : ''} onClick={() => setBrowserMode('list')}>列表</button>
              <button className={browserMode === 'thumb' ? 'active' : ''} onClick={() => setBrowserMode('thumb')}>缩略图</button>
            </div>
            <label className="upload">
              上传
              <input type="file" multiple onChange={async (event) => {
                try {
                  if (event.target.files.length > 0) await onUpload(event.target.files);
                } catch (err) {
                  onError(err.message);
                } finally {
                  event.target.value = '';
                }
              }} />
            </label>
            <button className="ghost" onClick={() => setFolderDialog(true)}>新建文件夹</button>
            <button className="ghost" onClick={onRefresh}>{busy ? '刷新中...' : '刷新'}</button>
            <button className="ghost" disabled={selectedDownloadableItems.length === 0} onClick={downloadSelected}>下载</button>
            <button className="danger" disabled={selectedItems.length === 0} onClick={confirmBatchDelete}>删除</button>
            {isSFTPTarget && (
              <button className="ghost" onClick={() => setCommandDialog(true)}>
                <Terminal size={15} strokeWidth={2.2} />
                执行
              </button>
            )}
          </div>
          <div className="nas-target">
            <div className="path-jump">
              <input
                value={pathInput}
                onChange={(event) => setPathInput(event.target.value)}
                onBlur={commitPathInput}
                onKeyDown={(event) => {
                  if (event.key === 'Enter') {
                    event.currentTarget.blur();
                    commitPathInput();
                  }
                }}
                placeholder="/ 或 path/to"
              />
              <button
                className={pathHistoryOpen ? 'path-history-toggle active' : 'path-history-toggle'}
                type="button"
                aria-label="显示历史路径"
                aria-expanded={pathHistoryOpen}
                onMouseDown={(event) => event.preventDefault()}
                onClick={() => setPathHistoryOpen((open) => !open)}
              >
                <ChevronDown size={15} strokeWidth={2.2} />
              </button>
              {pathHistoryOpen && (
                <div className="path-history-menu" role="listbox" aria-label="历史路径">
                  {pathHistory.length === 0 ? (
                    <div className="path-history-empty">暂无历史路径</div>
                  ) : (
                    pathHistory.map((item) => (
                      <button
                        key={item || '/'}
                        type="button"
                        role="option"
                        onMouseDown={(event) => selectPathHistory(event, item)}
                      >
                        {item || '/'}
                      </button>
                    ))
                  )}
                </div>
              )}
            </div>
          </div>
        </div>

        <div className="nas-pathbar">
          <span className="path-label">路径</span>
          <button onClick={() => changePath('')}>/</button>
          {crumbs.map((part, index) => (
            <button key={`${part}-${index}`} onClick={() => changePath(crumbs.slice(0, index + 1).join('/'))}>{part}</button>
          ))}
          <span className="path-count">{entries.length} 项</span>
        </div>

        {browserMode === 'list' ? (
          <div className="file-table" role="table" aria-label="文件列表">
            <div className="file-row head" role="row">
              <span className="file-cell check">
                <input type="checkbox" checked={allSelected} disabled={selectableEntries.length === 0} onChange={toggleAll} />
              </span>
              <span>名称</span>
              <span>大小</span>
              <span>修改时间</span>
            </div>
            {entries.map((item) => (
              <div className={item.isDir ? 'file-row dir' : 'file-row'} role="row" key={`${item.special || 'file'}:${item.path}`}>
                <span className="file-cell check" role="cell">
                  <input
                    type="checkbox"
                    checked={selectedSet.has(item.path)}
                    disabled={Boolean(item.special)}
                    onChange={() => togglePath(item.path)}
                    onClick={(event) => event.stopPropagation()}
                  />
                </span>
                <div className="file-cell name-cell" role="cell">
                  {renderEntryName(item, 'list')}
                </div>
                <span className="file-cell size" role="cell">{item.isDir ? '-' : formatSize(item.size)}</span>
                <span className="file-cell time" role="cell">{item.modTime ? formatDateTime(item.modTime) : '-'}</span>
              </div>
            ))}
          </div>
        ) : (
          <div className="thumb-grid">
            {entries.map((item) => (
              <div className={item.isDir ? 'thumb-card dir' : 'thumb-card'} key={`${item.special || 'file'}:${item.path}`}>
                <label className="thumb-check">
                  <input
                    type="checkbox"
                    checked={selectedSet.has(item.path)}
                    disabled={Boolean(item.special)}
                    onChange={() => togglePath(item.path)}
                  />
                </label>
                <button className="thumb-preview" onClick={() => openEntry(item)}>
                  {isImage(item) ? <img src={item.url} alt={item.displayName} loading="lazy" /> : <span>{item.isDir ? 'DIR' : fileExt(item.displayName)}</span>}
                </button>
                <div className="thumb-meta">
                  <div className="thumb-name-row">
                    {renderEntryName(item, 'thumb')}
                  </div>
                  <span>{item.isDir ? '-' : formatSize(item.size)}</span>
                  <span>{item.modTime ? formatDateTime(item.modTime) : '-'}</span>
                </div>
              </div>
            ))}
          </div>
        )}
        {dragActive && (
          <div className="drop-overlay">
            <div>
              <strong>拖放上传</strong>
              <span>松开后上传到当前路径 {path ? `/${path}` : '/'}</span>
            </div>
          </div>
        )}
      </div>
      {folderDialog && (
        <FolderDialog
          path={path}
          onError={onError}
          onCancel={() => setFolderDialog(false)}
          onCreate={async (name) => {
            await onCreateFolder(joinObjectPath(path, name));
            setFolderDialog(false);
          }}
        />
      )}
      {preview && <PreviewDialog preview={preview} onClose={() => setPreview(null)} />}
      {commandDialog && (
        <CommandDialog
          target={selectedTarget}
          path={path}
          onClose={() => setCommandDialog(false)}
          onError={onError}
          onMessage={onMessage}
        />
      )}
    </section>
  );
}

function ConfigPanel({ configState, selectedTarget, setSelectedTarget, onSaved, onMessage, onWarn, onError }) {
  const [draft, setDraft] = useState(configState.config);
  const [serviceDialog, setServiceDialog] = useState(null);

  useEffect(() => setDraft(configState.config), [configState]);

  const s3Index = selectedTarget.startsWith('s3:') ? Number(selectedTarget.slice(3)) : -1;
  const webdavIndex = selectedTarget.startsWith('webdav:') ? Number(selectedTarget.slice(7)) : -1;
  const sftpIndex = selectedTarget.startsWith('sftp:') ? Number(selectedTarget.slice(5)) : -1;
  const selectedS3 = s3Index >= 0 ? draft.s3?.[s3Index] : null;
  const selectedWebDAV = webdavIndex >= 0 ? draft.webdav?.[webdavIndex] : null;
  const selectedSFTP = sftpIndex >= 0 ? draft.sftp?.[sftpIndex] : null;
  const selectedServiceName = serviceName(selectedTarget, selectedS3, selectedWebDAV, selectedSFTP);
  const currentDefaultTarget = effectiveDefaultTarget(draft);

  function update(field, value) {
    setDraft((current) => ({ ...current, [field]: value }));
  }

  async function save() {
    try {
      await persistConfig(draft);
    } catch (err) {
      onError(err.message);
    }
  }

  async function persistConfig(nextDraft) {
    const previousBasePath = normalizeBasePath(configState.runtime.basePath || '');
    const data = await api('/config', { method: 'PUT', body: JSON.stringify(nextDraft) });
    onSaved(data);
    const nextBasePath = normalizeBasePath(data.runtime.basePath || '');
    if (previousBasePath !== nextBasePath) {
      window.location.assign(`${nextBasePath}/admin/`);
    }
    return data;
  }

  async function saveService(values) {
    let nextDraft = draft;
    if (selectedTarget === 'local') {
      nextDraft = { ...draft, filePath: values.filePath, urlPrefix: values.urlPrefix };
    } else if (selectedTarget.startsWith('s3:')) {
      nextDraft = {
        ...draft,
        s3: (draft.s3 || []).map((item, itemIndex) => (itemIndex === s3Index ? { ...values } : item)),
      };
    } else if (selectedTarget.startsWith('webdav:')) {
      nextDraft = {
        ...draft,
        webdav: (draft.webdav || []).map((item, itemIndex) => (itemIndex === webdavIndex ? { ...values } : item)),
      };
    } else if (selectedTarget.startsWith('sftp:')) {
      nextDraft = {
        ...draft,
        sftp: (draft.sftp || []).map((item, itemIndex) => (itemIndex === sftpIndex ? { ...values } : item)),
      };
    }
    await persistConfig(syncDefaultTarget(nextDraft, effectiveDefaultTarget(draft)));
    setServiceDialog(null);
  }

  async function createS3(values) {
    const nextS3 = [...(draft.s3 || []), { ...values }];
    const createdIndex = nextS3.length - 1;
    const nextDraft = syncDefaultTarget({ ...draft, s3: nextS3 }, effectiveDefaultTarget(draft));
    await persistConfig(nextDraft);
    setSelectedTarget(`s3:${createdIndex}`);
    setServiceDialog(null);
  }

  async function createWebDAV(values) {
    const nextWebDAV = [...(draft.webdav || []), { ...values }];
    const createdIndex = nextWebDAV.length - 1;
    const nextDraft = syncDefaultTarget({ ...draft, webdav: nextWebDAV }, effectiveDefaultTarget(draft));
    await persistConfig(nextDraft);
    setSelectedTarget(`webdav:${createdIndex}`);
    setServiceDialog(null);
  }

  async function createSFTP(values) {
    const nextSFTP = [...(draft.sftp || []), { ...values }];
    const createdIndex = nextSFTP.length - 1;
    const nextDraft = syncDefaultTarget({ ...draft, sftp: nextSFTP }, effectiveDefaultTarget(draft));
    await persistConfig(nextDraft);
    setSelectedTarget(`sftp:${createdIndex}`);
    setServiceDialog(null);
  }

  async function deleteService() {
    if (selectedTarget === 'local') return;
    let nextDraft = draft;
    if (selectedTarget.startsWith('s3:')) {
      nextDraft = { ...draft, s3: (draft.s3 || []).filter((_, itemIndex) => itemIndex !== s3Index) };
    } else if (selectedTarget.startsWith('webdav:')) {
      nextDraft = { ...draft, webdav: (draft.webdav || []).filter((_, itemIndex) => itemIndex !== webdavIndex) };
    } else if (selectedTarget.startsWith('sftp:')) {
      nextDraft = { ...draft, sftp: (draft.sftp || []).filter((_, itemIndex) => itemIndex !== sftpIndex) };
    }
    await persistConfig(updateDefaultAfterDelete(draft, nextDraft, selectedTarget));
    setSelectedTarget('local');
    setServiceDialog(null);
  }

  async function makeDefaultTarget(target) {
    if (currentDefaultTarget === target) return;
    try {
      await persistConfig(syncDefaultTarget(draft, target));
      onMessage('默认文件服务已更新');
    } catch (err) {
      onError(err.message);
    }
  }

  function renderDefaultSwitch(target) {
    const checked = currentDefaultTarget === target;
    return (
      <button
        className={checked ? 'default-switch active' : 'default-switch'}
        type="button"
        role="switch"
        aria-checked={checked}
        aria-label={checked ? '当前默认文件服务' : '设为默认文件服务'}
        title={checked ? '当前默认文件服务' : '设为默认文件服务'}
        onClick={() => makeDefaultTarget(target)}
      >
        <span className="default-switch-track"><span className="default-switch-thumb" /></span>
        <span>{checked ? '默认' : '非默认'}</span>
      </button>
    );
  }

  return (
    <section className="panel config-panel">
      <div className="panel-head">
        <div>
          <p className="eyebrow">config</p>
          <h2>配置管理</h2>
        </div>
      </div>

      <div className="config-groups">
        <section className="config-section">
          <div className="config-section-head">
            <div>
              <h3>公共配置</h3>
              <p className="muted">鉴权、监听地址和上传命名规则。</p>
            </div>
          </div>
          <div className="form-grid">
            <Field label={fieldLabel('key')} value={draft.key} onChange={(value) => update('key', value)} />
            <Field label={fieldLabel('rename')} value={draft.rename} onChange={(value) => update('rename', value)} />
            <Field label={fieldLabel('host')} value={draft.host} onChange={(value) => update('host', value)} />
            <Field label={fieldLabel('port')} type="number" value={draft.port || ''} onChange={(value) => update('port', Number(value) || 0)} />
            <Field label={fieldLabel('basePath')} value={draft.basePath} onChange={(value) => update('basePath', value)} />
          </div>
          <div className="row-actions">
            <button onClick={save}>保存公共配置</button>
          </div>
        </section>

        <section className="config-section">
          <div className="config-section-head">
            <div>
              <h3>文件服务配置</h3>
              <p className="muted">{selectedServiceName}；保存或删除会直接写入 config.json。</p>
            </div>
            <div className="row-actions">
              <button className="ghost" onClick={() => setServiceDialog({ mode: 'create-s3' })}>新增 S3</button>
              <button className="ghost" onClick={() => setServiceDialog({ mode: 'create-webdav' })}>新增 WebDAV</button>
              <button className="ghost" onClick={() => setServiceDialog({ mode: 'create-sftp' })}>新增 SFTP</button>
            </div>
          </div>

          <div className="toolbar">
            <select value={selectedTarget} onChange={(event) => setSelectedTarget(event.target.value)}>
              {targetOptions(draft).map((target) => (
                <option key={target.id} value={target.id}>{target.name} · {target.type}</option>
              ))}
            </select>
            <button
              className="icon-button"
              aria-label="配置当前服务"
              title="配置当前服务"
              onClick={() => setServiceDialog({ mode: 'edit', target: selectedTarget })}
            >
              <SquarePen size={18} strokeWidth={2.2} />
            </button>
          </div>

          {selectedTarget === 'local' && (
            <div className="service-card">
              <div className="s3-title">
                <strong>Local 文件服务</strong>
                {renderDefaultSwitch('local')}
              </div>
              <p className="muted">当前根目录：{configState.runtime.localRoot}</p>
              <div className="summary-grid">
                <SummaryItem label={fieldLabel('filePath')} value={draft.filePath || '(默认当前工作目录)'} />
                <SummaryItem label={fieldLabel('urlPrefix')} value={draft.urlPrefix || '(按请求 Host 生成)'} />
              </div>
            </div>
          )}

          {selectedTarget !== 'local' && selectedS3 && (
            <div className="service-card">
              <div className="s3-title">
                <strong>{selectedS3.name || selectedS3.bucket || `S3 ${s3Index + 1}`}</strong>
                {renderDefaultSwitch(selectedTarget)}
              </div>
              <div className="summary-grid">
                <SummaryItem label={fieldLabel('bucket')} value={selectedS3.bucket || '-'} />
                <SummaryItem label={fieldLabel('region')} value={selectedS3.region || '-'} />
                <SummaryItem label={fieldLabel('endpoint')} value={selectedS3.endpoint || '-'} />
                <SummaryItem label={fieldLabel('uploadPath')} value={selectedS3.uploadPath || '-'} />
                <SummaryItem label={fieldLabel('urlPrefix')} value={selectedS3.urlPrefix || '-'} />
                <SummaryItem label={fieldLabel('accessKeyID')} value={selectedS3.accessKeyID || '-'} />
              </div>
            </div>
          )}

          {selectedTarget.startsWith('webdav:') && selectedWebDAV && (
            <div className="service-card">
              <div className="s3-title">
                <strong>{selectedWebDAV.name || selectedWebDAV.endpoint || `WebDAV ${webdavIndex + 1}`}</strong>
                {renderDefaultSwitch(selectedTarget)}
              </div>
              <div className="summary-grid">
                <SummaryItem label={fieldLabel('endpoint')} value={selectedWebDAV.endpoint || '-'} />
                <SummaryItem label={fieldLabel('rootPath')} value={selectedWebDAV.rootPath || '-'} />
                <SummaryItem label={fieldLabel('uploadPath')} value={selectedWebDAV.uploadPath || '-'} />
                <SummaryItem label={fieldLabel('urlPrefix')} value={selectedWebDAV.urlPrefix || '-'} />
                <SummaryItem label={fieldLabel('username')} value={selectedWebDAV.username || '-'} />
              </div>
            </div>
          )}

          {selectedTarget.startsWith('sftp:') && selectedSFTP && (
            <div className="service-card">
              <div className="s3-title">
                <strong>{selectedSFTP.name || selectedSFTP.host || `SFTP ${sftpIndex + 1}`}</strong>
                {renderDefaultSwitch(selectedTarget)}
              </div>
              <div className="summary-grid">
                <SummaryItem label={fieldLabel('host')} value={selectedSFTP.host || '-'} />
                <SummaryItem label={fieldLabel('port')} value={selectedSFTP.port || 22} />
                <SummaryItem label={fieldLabel('rootPath')} value={selectedSFTP.rootPath || '-'} />
                <SummaryItem label={fieldLabel('uploadPath')} value={selectedSFTP.uploadPath || '-'} />
                <SummaryItem label={fieldLabel('urlPrefix')} value={selectedSFTP.urlPrefix || '-'} />
                <SummaryItem label={fieldLabel('username')} value={selectedSFTP.username || '-'} />
                <SummaryItem label={fieldLabel('privateKey')} value={selectedSFTP.privateKey ? '已配置' : '未配置'} />
              </div>
            </div>
          )}
        </section>
      </div>

      {serviceDialog && (
        <ServiceConfigDialog
          key={`${serviceDialog.mode}:${serviceDialog.target || 'new'}`}
          title={serviceDialog.mode === 'create-s3' ? '新增 S3 文件服务' : serviceDialog.mode === 'create-webdav' ? '新增 WebDAV 文件服务' : serviceDialog.mode === 'create-sftp' ? '新增 SFTP 文件服务' : `配置 ${selectedServiceName}`}
          mode={serviceDialog.mode}
          target={serviceDialog.target || selectedTarget}
          runtime={configState.runtime}
          localValues={{ filePath: draft.filePath || '', urlPrefix: draft.urlPrefix || '' }}
          s3Values={serviceDialog.mode === 'create-s3' ? emptyS3 : selectedS3}
          webdavValues={serviceDialog.mode === 'create-webdav' ? emptyWebDAV : selectedWebDAV}
          sftpValues={serviceDialog.mode === 'create-sftp' ? emptySFTP : selectedSFTP}
          onCancel={() => setServiceDialog(null)}
          onSave={serviceDialog.mode === 'create-s3' ? createS3 : serviceDialog.mode === 'create-webdav' ? createWebDAV : serviceDialog.mode === 'create-sftp' ? createSFTP : saveService}
          onDelete={deleteService}
          onMessage={onMessage}
          onWarn={onWarn}
          onError={onError}
        />
      )}
    </section>
  );
}

function ServiceConfigDialog({ title, mode, target, runtime, localValues, s3Values, webdavValues, sftpValues, onCancel, onSave, onDelete, onMessage, onWarn, onError }) {
  const isCreateS3 = mode === 'create-s3';
  const isCreateWebDAV = mode === 'create-webdav';
  const isCreateSFTP = mode === 'create-sftp';
  const isLocal = target === 'local' && !isCreateS3 && !isCreateWebDAV && !isCreateSFTP;
  const isWebDAV = isCreateWebDAV || target.startsWith('webdav:');
  const isSFTP = isCreateSFTP || target.startsWith('sftp:');
  const remoteDefaults = isSFTP ? emptySFTP : isWebDAV ? emptyWebDAV : emptyS3;
  const remoteValues = isSFTP ? sftpValues : isWebDAV ? webdavValues : s3Values;
  const [values, setValues] = useState(isLocal ? localValues : { ...remoteDefaults, ...(remoteValues || {}) });
  const [saving, setSaving] = useState(false);
  const [testing, setTesting] = useState(false);
  const [pendingHostKeyFingerprint, setPendingHostKeyFingerprint] = useState('');
  const [hostKeyConfirm, setHostKeyConfirm] = useState(false);
  const valuesRef = useRef(values);
  useEffect(() => {
    valuesRef.current = values;
  }, [values]);

  function update(field, value) {
    if (isSFTP && (field === 'host' || field === 'port')) {
      setPendingHostKeyFingerprint('');
      onWarn('服务器地址已变化，请重新测试并接受服务器指纹。');
      const nextValues = {
        ...valuesRef.current,
        [field]: field === 'port' ? Number(value) || 0 : value,
        hostKeyFingerprint: '',
      };
      valuesRef.current = nextValues;
      setValues(nextValues);
      return;
    }
    const nextValues = { ...valuesRef.current, [field]: field === 'port' ? Number(value) || 0 : value };
    valuesRef.current = nextValues;
    setValues(nextValues);
  }

  function acceptHostKey() {
    const nextValues = {
      ...valuesRef.current,
      hostKeyFingerprint: pendingHostKeyFingerprint,
    };
    valuesRef.current = nextValues;
    setValues(nextValues);
    setPendingHostKeyFingerprint('');
    onWarn('服务器指纹已接受，请再次测试验证登录。');
  }

  async function handleSave() {
    setSaving(true);
    let failed = false;
    try {
      if (isSFTP && !String(valuesRef.current.hostKeyFingerprint || '').trim()) {
        throw new Error('请先测试连通性并接受服务器指纹');
      }
      await onSave(valuesRef.current);
    } catch (err) {
      failed = true;
      onError(err.message);
    } finally {
      if (failed) setSaving(false);
    }
  }

  async function handleDelete() {
    setSaving(true);
    try {
      await onDelete();
    } catch (err) {
      onError(err.message);
      setSaving(false);
    }
  }

  async function testRemote() {
    setTesting(true);
    setPendingHostKeyFingerprint('');
    setHostKeyConfirm(false);
    try {
      const path = isSFTP ? '/sftp/test' : isWebDAV ? '/webdav/test' : '/s3/test';
      const data = await api(path, { method: 'POST', body: JSON.stringify(valuesRef.current) });
      if (isSFTP && data.hostKeyChanged && data.hostKeyFingerprint) {
        setPendingHostKeyFingerprint(data.hostKeyFingerprint);
        setHostKeyConfirm(true);
        onWarn('服务器指纹发生变化，请确认可信后再接受。');
        return;
      }
      if (isSFTP && data.hostKeyCaptured && data.hostKeyFingerprint) {
        setPendingHostKeyFingerprint(data.hostKeyFingerprint);
        setHostKeyConfirm(true);
        onWarn('已捕获服务器指纹，请确认可信后再接受。');
        return;
      }
      onMessage(data.message || `${isSFTP ? 'SFTP' : isWebDAV ? 'WebDAV' : 'S3'} 连通`);
    } catch (err) {
      onError(err.message);
    } finally {
      setTesting(false);
    }
  }

  return createPortal(
    <div className="dialog-backdrop" role="presentation">
      <section className="dialog config-dialog" role="dialog" aria-modal="true" aria-labelledby="service-config-title">
        <div className="preview-head">
          <div>
            <p className="eyebrow">file service</p>
            <h3 id="service-config-title">{title}</h3>
          </div>
          <button className="icon-close" aria-label="关闭配置" title="关闭" onClick={onCancel}>×</button>
        </div>

        {isLocal ? (
          <>
            <p className="muted">当前运行根目录：{runtime.localRoot}</p>
            <div className="form-grid">
              <Field label={fieldLabel('filePath')} value={values.filePath} onChange={(value) => update('filePath', value)} />
              <Field label={fieldLabel('urlPrefix')} value={values.urlPrefix} onChange={(value) => update('urlPrefix', value)} />
            </div>
          </>
        ) : (
          <>
            <div className="form-grid">
              {Object.keys(remoteDefaults)
                .filter((field) => field !== 'hostKeyFingerprint')
                .map((field) => (
                  <Field
                    key={field}
                    label={fieldLabel(field)}
                    type={field === 'port' ? 'number' : 'text'}
                    multiline={field === 'privateKey'}
                    wide={field === 'privateKey'}
                    value={values[field] || ''}
                    onChange={(value) => update(field, value)}
                  />
                ))}
            </div>
          </>
        )}

        <div className="row-actions">
          {!isLocal && !isCreateS3 && !isCreateWebDAV && !isCreateSFTP && <button className="danger" disabled={saving} onClick={handleDelete}>{saving ? '删除中...' : '删除配置'}</button>}
          {!isLocal && <button className="ghost" disabled={testing || saving} onClick={testRemote}>{testing ? '测试中...' : '测试连通性'}</button>}
          <button className="ghost" disabled={saving} onClick={onCancel}>取消</button>
          <button disabled={saving} onClick={handleSave}>{saving ? '保存中...' : '保存'}</button>
        </div>
        {hostKeyConfirm && (
          <ConfirmDialog
            title="接受服务器指纹"
            message={`确认信任 ${values.host || '该主机'}:${values.port || 22} 的 SFTP 服务器指纹？${pendingHostKeyFingerprint}`}
            confirmText="接受"
            onCancel={() => setHostKeyConfirm(false)}
            onConfirm={() => {
              setHostKeyConfirm(false);
              acceptHostKey();
            }}
          />
        )}
      </section>
    </div>,
    document.body,
  );
}

function SummaryItem({ label, value }) {
  return (
    <div className="summary-item">
      <span>{label}</span>
      <strong>{value}</strong>
    </div>
  );
}

function ConfirmDialog({ title, message, confirmText = '确认', onCancel, onConfirm }) {
  return createPortal(
    <div className="dialog-backdrop" role="presentation">
      <section className="dialog" role="dialog" aria-modal="true" aria-labelledby="confirm-title">
        <p className="eyebrow">confirm</p>
        <h3 id="confirm-title">{title}</h3>
        <p className="muted">{message}</p>
        <div className="row-actions">
          <button className="ghost" onClick={onCancel}>取消</button>
          <button className="danger" onClick={onConfirm}>{confirmText}</button>
        </div>
      </section>
    </div>,
    document.body,
  );
}

const commandHistoryKey = 'upimg:sftp-command-history';

function CommandDialog({ target, path, onClose, onError, onMessage }) {
  const [command, setCommand] = useState('');
  const [history, setHistory] = useState(() => loadCommandHistory());
  const [historyOpen, setHistoryOpen] = useState(false);
  const [output, setOutput] = useState([]);
  const [connected, setConnected] = useState(false);
  const [status, setStatus] = useState('正在连接...');
  const [actualPath, setActualPath] = useState('');
  const [maximized, setMaximized] = useState(false);
  const socketRef = useRef(null);
  const terminalRef = useRef(null);
  const connectedRef = useRef(false);

  useEffect(() => {
    const url = adminSFTPTerminalURL(target);
    const socket = new WebSocket(url);
    socketRef.current = socket;
    const connectTimer = window.setTimeout(() => {
      if (socket.readyState !== WebSocket.CONNECTING) return;
      setStatus('WebSocket 仍在连接');
      appendOutput({ type: 'warning', data: `WebSocket 连接尚未建立：${url}\n` });
    }, 5000);
    socket.onopen = () => {
      window.clearTimeout(connectTimer);
      setStatus('正在建立 SSH 连接...');
      socket.send(JSON.stringify({ type: 'init', path }));
    };
    socket.onmessage = (event) => {
      const message = parseJSON(event.data);
      if (!message) return;
      if (message.type === 'ready') {
        connectedRef.current = true;
        setConnected(true);
        setActualPath(message.actualPath || '');
        setStatus(message.message || 'SSH 已连接');
        appendOutput({ type: 'status', data: `${message.message || 'SSH 已连接'}\n` });
        return;
      }
      if (message.type === 'stdout' || message.type === 'stderr') {
        appendOutput({ type: message.type, data: cleanTerminalOutput(message.data || '') });
        return;
      }
      if (message.type === 'status') {
        setStatus(message.message || 'SSH 已连接');
        appendOutput({ type: 'status', data: `${message.message || 'status'}\n` });
        return;
      }
      if (message.type === 'error') {
        setStatus('执行失败');
        appendOutput({ type: 'error', data: `${message.message || '执行失败'}\n` });
        return;
      }
      if (message.type === 'done') {
        connectedRef.current = false;
        setConnected(false);
        setStatus('SSH 连接已关闭');
        appendOutput({ type: 'warning', data: 'SSH 连接已关闭\n' });
      }
    };
    socket.onerror = () => {
      window.clearTimeout(connectTimer);
      connectedRef.current = false;
      setConnected(false);
      setStatus('连接失败');
      appendOutput({ type: 'error', data: `WebSocket 或 SSH 连接失败：${url}\n` });
      onError('命令终端连接失败');
    };
    socket.onclose = () => {
      window.clearTimeout(connectTimer);
      if (socketRef.current === socket) socketRef.current = null;
      if (connectedRef.current) {
        appendOutput({ type: 'warning', data: '连接已关闭\n' });
      }
      connectedRef.current = false;
      setConnected(false);
      setStatus((current) => (current === '连接失败' ? current : '连接已关闭'));
    };
    return () => {
      window.clearTimeout(connectTimer);
      closeTerminalSocket(socket);
    };
  }, [target, path]);

  useEffect(() => {
    terminalRef.current?.scrollTo({ top: terminalRef.current.scrollHeight });
  }, [output]);

  function appendOutput(entry) {
    setOutput((current) => [...current, { id: `${Date.now()}-${current.length}`, ...entry }]);
  }

  function selectHistory(item) {
    setCommand(item);
    setHistoryOpen(false);
  }

  function runCommand(event) {
    event.preventDefault();
    const nextCommand = command.trim();
    const socket = socketRef.current;
    if (!nextCommand || !connected || !socket || socket.readyState !== WebSocket.OPEN) return;

    const nextHistory = saveCommandHistory(nextCommand, history);
    setHistory(nextHistory);
    setHistoryOpen(false);
    appendOutput({ type: 'command', data: `$ ${nextCommand}\n` });
    socket.send(JSON.stringify({ type: 'command', command: nextCommand }));
    setCommand('');
  }

  function interruptCommand() {
    const socket = socketRef.current;
    if (!socket || socket.readyState !== WebSocket.OPEN) return;
    socket.send(JSON.stringify({ type: 'interrupt' }));
    setStatus('正在中断...');
  }

  function closeDialog() {
    closeTerminalSocket(socketRef.current);
    onClose();
  }

  return createPortal(
    <div className="dialog-backdrop" role="presentation">
      <section className={maximized ? 'dialog command-dialog maximized' : 'dialog command-dialog'} role="dialog" aria-modal="true" aria-labelledby="command-title">
        <div className="command-head">
          <div>
            <p className="eyebrow">终端<span>{actualPath ? ` (${actualPath})` : ''}</span></p>
          </div>
          <div className="command-window-actions">
            <button className="tiny-icon" type="button" onClick={() => setOutput([])} aria-label="清空终端内容" title="清空终端内容">
              <Eraser size={16} strokeWidth={2.2} />
            </button>
            <button className="tiny-icon" type="button" onClick={() => setMaximized((value) => !value)} aria-label={maximized ? '还原窗口' : '最大化窗口'} title={maximized ? '还原窗口' : '最大化窗口'}>
              {maximized ? <Minimize2 size={16} strokeWidth={2.2} /> : <Maximize2 size={16} strokeWidth={2.2} />}
            </button>
            <button className="icon-close" type="button" onClick={closeDialog} aria-label="关闭命令终端">
              <X size={18} strokeWidth={2.2} />
            </button>
          </div>
        </div>
        <form className="command-form" onSubmit={runCommand}>
          <label className="field command-field">
            <div className="command-input-wrap">
              <input
                autoFocus
                value={command}
                disabled={!connected}
                onChange={(event) => setCommand(event.target.value)}
                onKeyDown={(event) => {
                  if (event.key === 'Escape') setHistoryOpen(false);
                }}
                placeholder="例如 cd app && docker compose up"
              />
              <button
                className={historyOpen ? 'path-history-toggle active' : 'path-history-toggle'}
                type="button"
                disabled={!connected}
                aria-label="显示历史命令"
                aria-expanded={historyOpen}
                onMouseDown={(event) => event.preventDefault()}
                onClick={() => setHistoryOpen((open) => !open)}
              >
                <ChevronDown size={15} strokeWidth={2.2} />
              </button>
              <button className="path-history-toggle danger-icon" type="button" disabled={!connected} onClick={interruptCommand} aria-label="中断当前命令" title="中断当前命令">
                <OctagonX size={15} strokeWidth={2.2} />
              </button>
              {historyOpen && (
                <div className="command-history-menu" role="listbox" aria-label="历史命令">
                  {history.length === 0 ? (
                    <div className="path-history-empty">暂无历史命令</div>
                  ) : (
                    history.map((item) => (
                      <button key={item} type="button" role="option" onMouseDown={() => selectHistory(item)}>
                        {item}
                      </button>
                    ))
                  )}
                </div>
              )}
            </div>
          </label>
        </form>
        <div className="terminal-frame" ref={terminalRef} aria-live="polite" aria-label="终端输出">
          {output.length === 0 ? (
            <span className="terminal-empty">命令输出会显示在这里。</span>
          ) : (
            output.map((item) => (
              <span className={`terminal-line ${item.type}`} key={item.id}>{item.data}</span>
            ))
          )}
        </div>
      </section>
    </div>,
    document.body,
  );
}

function closeTerminalSocket(socket) {
  if (!socket) return;
  if (socket.readyState === WebSocket.OPEN) {
    socket.send(JSON.stringify({ type: 'close' }));
  }
  if (socket.readyState === WebSocket.OPEN || socket.readyState === WebSocket.CONNECTING) {
    socket.close();
  }
}

function cleanTerminalOutput(value) {
  return String(value || '')
    .replace(/\x1b\][^\x07\x1b]*(?:\x07|\x1b\\)/g, '')
    .replace(/\x1b\[[0-?]*[ -/]*[@-~]/g, '')
    .replace(/\x1b[()][A-Za-z0-9]/g, '')
    .replace(/\[\?2004[hl]/g, '')
    .replace(/\]0;[^\x07]*(?:\x07|$)/g, '')
    .replace(/\]1337;[^\x07]*(?:\x07|$)/g, '')
    .replace(/\x07/g, '')
    .replace(/\r(?!\n)/g, '\n');
}

function loadCommandHistory() {
  try {
    const value = JSON.parse(window.localStorage.getItem(commandHistoryKey) || '[]');
    return Array.isArray(value) ? value.filter((item) => typeof item === 'string').slice(0, 20) : [];
  } catch {
    return [];
  }
}

function saveCommandHistory(command, current) {
  const next = [command, ...current.filter((item) => item !== command)].slice(0, 20);
  try {
    window.localStorage.setItem(commandHistoryKey, JSON.stringify(next));
  } catch {
    // 历史记录只是便捷能力，存储失败不影响命令执行。
  }
  return next;
}

function FolderDialog({ path, onError, onCancel, onCreate }) {
  const [name, setName] = useState('');
  const [saving, setSaving] = useState(false);

  async function handleSubmit(event) {
    event.preventDefault();
    const trimmed = name.trim();
    const validationError = validateFolderName(trimmed);
    if (validationError) {
      onError(validationError);
      return;
    }
    setSaving(true);
    try {
      await onCreate(trimmed);
    } catch (err) {
      onError(err.message);
      setSaving(false);
    }
  }

  return createPortal(
    <div className="dialog-backdrop" role="presentation">
      <form className="dialog" role="dialog" aria-modal="true" aria-labelledby="folder-title" onSubmit={handleSubmit}>
        <p className="eyebrow">folder</p>
        <h3 id="folder-title">新建文件夹</h3>
        <p className="muted">将在 {path ? `/${path}` : '/'} 下创建。</p>
        <label className="field">
          <span>文件夹名称</span>
          <input autoFocus value={name} onChange={(event) => setName(event.target.value)} placeholder="例如 images" />
        </label>
        <div className="row-actions">
          <button type="button" className="ghost" disabled={saving} onClick={onCancel}>取消</button>
          <button type="submit" disabled={saving}>{saving ? '创建中...' : '创建'}</button>
        </div>
      </form>
    </div>,
    document.body,
  );
}

function DeleteOverlay({ message }) {
  return createPortal(
    <div className="delete-overlay" role="alert" aria-live="assertive" aria-busy="true">
      <div className="delete-card">
        <span className="delete-spinner" aria-hidden="true" />
        <h3>{message || '正在删除...'}</h3>
        <p>请勿关闭页面或重复操作，删除完成后会自动刷新列表。</p>
      </div>
    </div>,
    document.body,
  );
}

function ExtractOverlay({ item, path }) {
  return createPortal(
    <div className="extract-overlay" role="alert" aria-live="assertive" aria-busy="true">
      <div className="extract-card">
        <div className="extract-animation" aria-hidden="true">
          <span />
          <span />
          <span />
        </div>
        <h3>正在解压缩 {item?.displayName || basename(item?.path || '')}</h3>
        <p>目标目录：{path ? `/${path}` : '/'}。请勿关闭页面或重复操作，完成后会自动刷新列表。</p>
      </div>
    </div>,
    document.body,
  );
}

function UploadDialog({ upload, onClose }) {
  const doneCount = upload.items.filter((item) => item.status === 'done').length;
  const failedCount = upload.items.filter((item) => item.status === 'failed').length;
  const finishedCount = doneCount + failedCount;
  const total = upload.items.length;

  return createPortal(
    <div className="upload-overlay" role="dialog" aria-modal="true" aria-labelledby="upload-title" aria-busy={!upload.finished}>
      <section className="upload-dialog">
        <div className="upload-dialog-head">
          <div>
            <p className="eyebrow">upload</p>
            <h3 id="upload-title">{upload.finished ? '上传已结束' : '正在上传文件'}</h3>
          </div>
          <span className={failedCount > 0 ? 'upload-summary bad' : 'upload-summary'}>
            {finishedCount}/{total}
          </span>
        </div>
        <div className="upload-list">
          {upload.items.map((item) => (
            <div className={`upload-item ${item.status}`} key={item.id}>
              <div className="upload-item-meta">
                <strong>{item.name}</strong>
                <span>{formatSize(item.size)} · {uploadStatusText(item)}</span>
              </div>
              <div className="upload-progress" aria-label={`${item.name} 上传进度`}>
                <span style={{ width: `${Math.max(0, Math.min(100, item.progress || 0))}%` }} />
              </div>
              {item.message && item.status === 'failed' && <p>{item.message}</p>}
            </div>
          ))}
        </div>
        <div className="row-actions">
          <button disabled={!upload.finished} onClick={onClose}>{upload.finished ? '关闭' : '上传中...'}</button>
        </div>
      </section>
    </div>,
    document.body,
  );
}

function uploadStatusText(item) {
  if (item.status === 'done') return '完成';
  if (item.status === 'failed') return '失败';
  if (item.status === 'uploading') return `${item.progress || 0}%`;
  return '等待中';
}

function PreviewDialog({ preview, onClose }) {
  const [imageScale, setImageScale] = useState(1);
  const [imageOffset, setImageOffset] = useState({ x: 0, y: 0 });
  const [dragStart, setDragStart] = useState(null);

  useEffect(() => {
    function handleKeyDown(event) {
      if (event.key === 'Escape') onClose();
    }
    window.addEventListener('keydown', handleKeyDown);
    return () => window.removeEventListener('keydown', handleKeyDown);
  }, [onClose]);

  function zoom(delta) {
    setImageScale((current) => Math.min(5, Math.max(0.2, Number((current + delta).toFixed(2)))));
  }

  function resetImageView() {
    setImageScale(1);
    setImageOffset({ x: 0, y: 0 });
  }

  if (preview.type === 'image') {
    return createPortal(
      <div className="image-viewer" role="dialog" aria-modal="true" aria-label={preview.title}>
        <div className="image-viewer-actions">
          <button onClick={() => zoom(-0.1)}>缩小</button>
          <span>{Math.round(imageScale * 100)}%</span>
          <button onClick={() => zoom(0.1)}>放大</button>
          <button onClick={resetImageView}>重置</button>
          <button className="image-viewer-close" aria-label="关闭预览" title="关闭" onClick={onClose}>×</button>
        </div>
        <div className="image-viewer-stage" onWheel={(event) => {
          event.preventDefault();
          zoom(event.deltaY > 0 ? -0.1 : 0.1);
        }} onPointerDown={(event) => {
          event.currentTarget.setPointerCapture(event.pointerId);
          setDragStart({
            pointerId: event.pointerId,
            x: event.clientX,
            y: event.clientY,
            offset: imageOffset,
          });
        }} onPointerMove={(event) => {
          if (!dragStart || dragStart.pointerId !== event.pointerId) return;
          setImageOffset({
            x: dragStart.offset.x + event.clientX - dragStart.x,
            y: dragStart.offset.y + event.clientY - dragStart.y,
          });
        }} onPointerUp={(event) => {
          if (dragStart?.pointerId === event.pointerId) setDragStart(null);
        }} onPointerCancel={() => {
          setDragStart(null);
        }}>
          <img
            src={preview.url}
            alt={preview.title}
            draggable="false"
            style={{ transform: `translate(${imageOffset.x}px, ${imageOffset.y}px) scale(${imageScale})` }}
          />
        </div>
      </div>,
      document.body,
    );
  }

  return createPortal(
    <div className="dialog-backdrop preview-backdrop" role="presentation">
      <section className="dialog preview-dialog" role="dialog" aria-modal="true" aria-labelledby="preview-title">
        <div className="preview-head">
          <div>
            <p className="eyebrow">preview</p>
            <h3 id="preview-title">{preview.title}</h3>
          </div>
          <button className="icon-close" aria-label="关闭预览" title="关闭" onClick={onClose}>×</button>
        </div>
        {preview.type === 'text' && (
          <div className="text-preview">
            {preview.loading && <p className="muted">正在加载文本...</p>}
            {typeof preview.text === 'string' && <pre>{preview.text}</pre>}
          </div>
        )}
      </section>
    </div>,
    document.body,
  );
}

function targetOptions(draft) {
  const options = [{
    id: 'local',
    type: 'local',
    name: 'Local',
  }];
  (draft.s3 || []).forEach((item, index) => {
    options.push({
      id: `s3:${index}`,
      type: 'aws-s3',
      name: item.name || item.bucket || `S3 ${index + 1}`,
    });
  });
  (draft.webdav || []).forEach((item, index) => {
    options.push({
      id: `webdav:${index}`,
      type: 'webdav',
      name: item.name || item.endpoint || `WebDAV ${index + 1}`,
    });
  });
  (draft.sftp || []).forEach((item, index) => {
    options.push({
      id: `sftp:${index}`,
      type: 'sftp',
      name: item.name || item.host || `SFTP ${index + 1}`,
    });
  });
  return options;
}

function selectedTargetID(targets) {
  return targets?.find((target) => target.selected)?.id || targets?.[0]?.id || 'local';
}

function serviceName(target, s3, webdav, sftp) {
  if (target === 'local') return 'Local 文件服务';
  if (target.startsWith('s3:')) {
    const index = Number(target.slice(3));
    return s3?.name || s3?.bucket || `S3 ${index + 1}`;
  }
  if (target.startsWith('webdav:')) {
    const index = Number(target.slice(7));
    return webdav?.name || webdav?.endpoint || `WebDAV ${index + 1}`;
  }
  if (target.startsWith('sftp:')) {
    const index = Number(target.slice(5));
    return sftp?.name || sftp?.host || `SFTP ${index + 1}`;
  }
  return target;
}

function effectiveDefaultTarget(draft) {
  if (draft.defaultTarget) return draft.defaultTarget;
  return 'local';
}

function syncDefaultTarget(draft, target) {
  const defaultTarget = target || effectiveDefaultTarget(draft);
  return {
    ...draft,
    defaultTarget,
    s3: draft.s3 || [],
    webdav: draft.webdav || [],
    sftp: draft.sftp || [],
  };
}

function updateDefaultAfterDelete(previousDraft, nextDraft, deletedTarget) {
  const previousDefault = effectiveDefaultTarget(previousDraft);
  const defaultTarget = remapDefaultAfterDelete(previousDefault, deletedTarget);
  return {
    ...nextDraft,
    defaultTarget,
    s3: nextDraft.s3 || [],
    webdav: nextDraft.webdav || [],
    sftp: nextDraft.sftp || [],
  };
}

function remapDefaultAfterDelete(defaultTarget, deletedTarget) {
  if (defaultTarget === deletedTarget) return 'local';
  const defaultInfo = parseIndexedTarget(defaultTarget);
  const deletedInfo = parseIndexedTarget(deletedTarget);
  if (!defaultInfo || !deletedInfo || defaultInfo.type !== deletedInfo.type) return defaultTarget;
  if (defaultInfo.index > deletedInfo.index) return `${defaultInfo.type}:${defaultInfo.index - 1}`;
  return defaultTarget;
}

function parseIndexedTarget(target) {
  const match = /^(s3|webdav|sftp):(\d+)$/.exec(target || '');
  if (!match) return null;
  return { type: match[1], index: Number(match[2]) };
}

function fieldLabel(field) {
  return fieldLabels[field] || field;
}

function Field({ label, value, onChange, type = 'text', multiline = false, wide = false }) {
  return (
    <label className={wide ? 'field wide' : 'field'}>
      <span>{label}</span>
      {multiline ? (
        <textarea value={value ?? ''} onChange={(event) => onChange(event.target.value)} />
      ) : (
        <input type={type} value={value ?? ''} onChange={(event) => onChange(event.target.value)} />
      )}
    </label>
  );
}

function formatSize(value) {
  if (!value) return '0 B';
  const units = ['B', 'KB', 'MB', 'GB'];
  let size = value;
  let index = 0;
  while (size >= 1024 && index < units.length - 1) {
    size /= 1024;
    index += 1;
  }
  return `${size.toFixed(index === 0 ? 0 : 1)} ${units[index]}`;
}

function formatDateTime(value) {
  const date = new Date(value);
  if (Number.isNaN(date.getTime()) || date.getTime() === 0) return '-';
  return date.toLocaleString('zh-CN', {
    year: 'numeric',
    month: '2-digit',
    day: '2-digit',
    hour: '2-digit',
    minute: '2-digit',
  });
}

function basename(value) {
  return String(value || '').replace(/\/$/, '').split('/').filter(Boolean).pop() || value;
}

function fileExt(value) {
  const name = basename(value);
  const index = name.lastIndexOf('.');
  if (index < 0 || index === name.length - 1) return '文件';
  return name.slice(index + 1).toUpperCase();
}

function isImage(item) {
  return Boolean(item?.url) && /\.(avif|bmp|gif|ico|jpe?g|png|svg|webp)$/i.test(item.path || '');
}

function isTextFile(item) {
  return Boolean(item?.url) && /\.(conf|css|csv|env|go|htm|html|ini|js|json|jsx|log|md|py|rs|sh|sql|toml|ts|tsx|txt|xml|ya?ml)$/i.test(item.path || '');
}

function isArchiveFile(item) {
  return /\.(zip|tar|tar\.gz|tgz|tar\.bz2|tbz2|tar\.xz|txz|gz|bz2|xz)$/i.test(item?.path || '');
}

function triggerDownload(url, fileName) {
  const link = document.createElement('a');
  link.href = url;
  link.download = fileName || '';
  link.style.display = 'none';
  document.body.appendChild(link);
  link.click();
  link.remove();
}

function joinObjectPath(dir, name) {
  const cleanDir = String(dir || '').replace(/^\/+|\/+$/g, '');
  const cleanName = String(name || '').replace(/^\/+|\/+$/g, '');
  return cleanDir ? `${cleanDir}/${cleanName}` : cleanName;
}

function normalizeObjectPath(value) {
  const normalized = String(value || '').trim().replace(/\\/g, '/').replace(/^\/+|\/+$/g, '');
  if (!normalized || normalized === '.') return '';
  return normalized.split('/').filter((part) => part && part !== '.').join('/');
}

function addPathHistory(current, path) {
  const normalized = normalizeObjectPath(path);
  if (normalized === '') {
    return current.includes('') ? current : ['', ...current].slice(0, 20);
  }
  if (current.includes(normalized)) return current;
  return [normalized, ...current].slice(0, 20);
}

function validateFolderName(value) {
  if (!value) return '文件夹名称不能为空';
  if (value === '.' || value === '..') return '文件夹名称不能是 . 或 ..';
  if (/[\\/]/.test(value)) return '文件夹名称不能包含路径分隔符';
  if (value.includes('\0')) return '文件夹名称包含非法字符';
  return '';
}

function validateFileName(value) {
  if (!value) return '文件名不能为空';
  if (value === '.' || value === '..') return '文件名不能是 . 或 ..';
  if (/[\\/]/.test(value)) return '文件名不能包含路径分隔符';
  if (value.includes('\0')) return '文件名包含非法字符';
  return '';
}

function addVirtualFolder(current, target, path) {
  const folderPath = `${String(path || '').replace(/^\/+|\/+$/g, '')}/`;
  if (current.some((item) => item.target === target && item.path === folderPath)) return current;
  return [...current, { target, path: folderPath }];
}

function removeVirtualFolders(current, target, paths) {
  const normalized = paths.map((path) => String(path || '').replace(/^\/+/, ''));
  return current.filter((item) => {
    if (item.target !== target) return true;
    return !normalized.some((path) => item.path === path || item.path.startsWith(path));
  });
}

function mergeVirtualFolders(objects, virtualFolders, target, dir) {
  const existing = new Set((objects || []).map((item) => item.path));
  const currentDir = String(dir || '').replace(/^\/+|\/+$/g, '');
  const prefix = currentDir ? `${currentDir}/` : '';
  const virtualObjects = virtualFolders
    .filter((item) => item.target === target)
    .map((item) => item.path)
    .filter((itemPath) => itemPath.startsWith(prefix))
    .filter((itemPath) => {
      const rest = itemPath.slice(prefix.length).replace(/\/$/, '');
      return rest !== '' && !rest.includes('/');
    })
    .filter((itemPath) => !existing.has(itemPath))
    .map((itemPath) => ({
      path: itemPath,
      url: '',
      size: 0,
      modTime: '',
      type: 'aws-s3',
      isDir: true,
      virtual: true,
    }));
  return [...virtualObjects, ...(objects || [])].sort((a, b) => {
    if (a.isDir !== b.isDir) return a.isDir ? -1 : 1;
    return a.path.localeCompare(b.path);
  });
}

createRoot(document.getElementById('root')).render(<App />);
