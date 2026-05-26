import React, { useEffect, useMemo, useRef, useState } from 'react';
import { createPortal } from 'react-dom';
import { createRoot } from 'react-dom/client';
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

async function api(path, options = {}) {
  const headers = options.body instanceof FormData ? {} : { 'Content-Type': 'application/json' };
  const response = await fetch(`${adminAPIBase()}${path}`, {
    credentials: 'same-origin',
    ...options,
    headers: { ...headers, ...(options.headers || {}) },
  });
  const data = await response.json().catch(() => ({ success: false, message: 'invalid server response' }));
  if (!response.ok || data.success === false) {
    throw new Error(data.message || `request failed: ${response.status}`);
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
  const [error, setError] = useState('');
  const [confirmState, setConfirmState] = useState(null);
  const [deleting, setDeleting] = useState(null);
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
    loadConfig();
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
    await api('/logout', { method: 'POST', body: '{}' });
    setSession({ authenticated: false });
    setConfigState(null);
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

  if (!session) {
    return <Shell message="正在连接服务..." />;
  }
  if (!authenticated) {
    return <Login onLogin={login} error={error} />;
  }
  if (!configState) {
    return <Shell message="正在加载配置..." />;
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
          <button className="ghost" onClick={() => setView(view === 'config' ? 'files' : 'config')}>
            {view === 'config' ? '返回文件' : '配置'}
          </button>
          <button className="ghost" onClick={logout}>退出</button>
        </div>
      </header>

      {(message || error) && <Toast type={error ? 'bad' : 'ok'} message={error || message} />}
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
          onError={setError}
        />
      )}
    </main>
  );
}

function Shell({ message }) {
  return <main className="center-card"><p>{message}</p></main>;
}

function Login({ onLogin, error }) {
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
        {error && <div className="notice bad">{error}</div>}
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

function FilePanel({ selectedTarget, path, setPath, objects, busy, onRefresh, onCreateFolder, onUpload, onBatchDelete, onError, onConfirm }) {
  const [browserMode, setBrowserMode] = useState('list');
  const [preview, setPreview] = useState(null);
  const [folderDialog, setFolderDialog] = useState(false);
  const [dragActive, setDragActive] = useState(false);
  const [pathInput, setPathInput] = useState(path);
  const [pathHistory, setPathHistory] = useState([]);
  const [selectedPaths, setSelectedPaths] = useState([]);
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

  useEffect(() => {
    setSelectedPaths([]);
  }, [selectedTarget, path, objects]);

  useEffect(() => {
    setPathInput(path);
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
        setPreview({ type: 'text', title: item.displayName, error: err.message });
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
            <button className="danger" disabled={selectedItems.length === 0} onClick={confirmBatchDelete}>删除</button>
          </div>
          <div className="nas-target">
            <label className="path-jump">
              <input
                list="path-history"
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
              <datalist id="path-history">
                {pathHistory.map((item) => (
                  <option key={item || '/'} value={item} label={item || '/'} />
                ))}
              </datalist>
            </label>
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
                <button className="file-cell name" role="cell" onClick={() => openEntry(item)}>
                  <span>{item.displayName}</span>
                </button>
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
                  <button className="thumb-name" onClick={() => openEntry(item)}>{item.displayName}</button>
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
          onCancel={() => setFolderDialog(false)}
          onCreate={async (name) => {
            await onCreateFolder(joinObjectPath(path, name));
            setFolderDialog(false);
          }}
        />
      )}
      {preview && <PreviewDialog preview={preview} onClose={() => setPreview(null)} />}
    </section>
  );
}

function ConfigPanel({ configState, selectedTarget, setSelectedTarget, onSaved, onError }) {
  const [draft, setDraft] = useState(configState.config);
  const [serviceDialog, setServiceDialog] = useState(null);

  useEffect(() => setDraft(configState.config), [configState]);

  const s3Index = selectedTarget.startsWith('s3:') ? Number(selectedTarget.slice(3)) : -1;
  const webdavIndex = selectedTarget.startsWith('webdav:') ? Number(selectedTarget.slice(7)) : -1;
  const selectedS3 = s3Index >= 0 ? draft.s3?.[s3Index] : null;
  const selectedWebDAV = webdavIndex >= 0 ? draft.webdav?.[webdavIndex] : null;
  const selectedServiceName = serviceName(selectedTarget, selectedS3, selectedWebDAV);
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
    }
    await persistConfig(syncDefaultTarget(nextDraft, selectedTarget));
    setServiceDialog(null);
  }

  async function createS3(values) {
    const nextS3 = [...(draft.s3 || []), { ...values }];
    const createdIndex = nextS3.length - 1;
    const nextDraft = syncDefaultTarget({ ...draft, s3: nextS3 }, `s3:${createdIndex}`);
    await persistConfig(nextDraft);
    setSelectedTarget(`s3:${createdIndex}`);
    setServiceDialog(null);
  }

  async function createWebDAV(values) {
    const nextWebDAV = [...(draft.webdav || []), { ...values }];
    const createdIndex = nextWebDAV.length - 1;
    const nextDraft = syncDefaultTarget({ ...draft, webdav: nextWebDAV }, `webdav:${createdIndex}`);
    await persistConfig(nextDraft);
    setSelectedTarget(`webdav:${createdIndex}`);
    setServiceDialog(null);
  }

  async function deleteService() {
    if (selectedTarget === 'local') return;
    const nextDraft = selectedTarget.startsWith('s3:')
      ? { ...draft, s3: (draft.s3 || []).filter((_, itemIndex) => itemIndex !== s3Index) }
      : { ...draft, webdav: (draft.webdav || []).filter((_, itemIndex) => itemIndex !== webdavIndex) };
    await persistConfig(updateDefaultAfterDelete(draft, nextDraft, selectedTarget));
    setSelectedTarget('local');
    setServiceDialog(null);
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
            <Field label="key" value={draft.key} onChange={(value) => update('key', value)} />
            <Field label="rename" value={draft.rename} onChange={(value) => update('rename', value)} />
            <Field label="host" value={draft.host} onChange={(value) => update('host', value)} />
            <Field label="port" type="number" value={draft.port || ''} onChange={(value) => update('port', Number(value) || 0)} />
            <Field label="basePath" value={draft.basePath} onChange={(value) => update('basePath', value)} />
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
              ⚙
            </button>
          </div>

          {selectedTarget === 'local' && (
            <div className="service-card">
              <div className="s3-title">
                <strong>Local 文件服务</strong>
                <span className={currentDefaultTarget === 'local' ? 'pill ok' : 'pill'}>{currentDefaultTarget === 'local' ? '默认' : '未设为默认'}</span>
              </div>
              <p className="muted">当前根目录：{configState.runtime.localRoot}</p>
              <div className="summary-grid">
                <SummaryItem label="filePath" value={draft.filePath || '(默认当前工作目录)'} />
                <SummaryItem label="urlPrefix" value={draft.urlPrefix || '(按请求 Host 生成)'} />
              </div>
            </div>
          )}

          {selectedTarget !== 'local' && selectedS3 && (
            <div className="service-card">
              <div className="s3-title">
                <strong>{selectedS3.name || selectedS3.bucket || `S3 ${s3Index + 1}`}</strong>
                <span className={currentDefaultTarget === selectedTarget ? 'pill ok' : 'pill'}>{currentDefaultTarget === selectedTarget ? '默认' : '未设为默认'}</span>
              </div>
              <div className="summary-grid">
                <SummaryItem label="bucket" value={selectedS3.bucket || '-'} />
                <SummaryItem label="region" value={selectedS3.region || '-'} />
                <SummaryItem label="endpoint" value={selectedS3.endpoint || '-'} />
                <SummaryItem label="uploadPath" value={selectedS3.uploadPath || '-'} />
                <SummaryItem label="urlPrefix" value={selectedS3.urlPrefix || '-'} />
                <SummaryItem label="accessKeyID" value={selectedS3.accessKeyID || '-'} />
              </div>
            </div>
          )}

          {selectedTarget.startsWith('webdav:') && selectedWebDAV && (
            <div className="service-card">
              <div className="s3-title">
                <strong>{selectedWebDAV.name || selectedWebDAV.endpoint || `WebDAV ${webdavIndex + 1}`}</strong>
                <span className={currentDefaultTarget === selectedTarget ? 'pill ok' : 'pill'}>{currentDefaultTarget === selectedTarget ? '默认' : '未设为默认'}</span>
              </div>
              <div className="summary-grid">
                <SummaryItem label="endpoint" value={selectedWebDAV.endpoint || '-'} />
                <SummaryItem label="rootPath" value={selectedWebDAV.rootPath || '-'} />
                <SummaryItem label="uploadPath" value={selectedWebDAV.uploadPath || '-'} />
                <SummaryItem label="urlPrefix" value={selectedWebDAV.urlPrefix || '-'} />
                <SummaryItem label="username" value={selectedWebDAV.username || '-'} />
              </div>
            </div>
          )}
        </section>
      </div>

      {serviceDialog && (
        <ServiceConfigDialog
          key={`${serviceDialog.mode}:${serviceDialog.target || 'new'}`}
          title={serviceDialog.mode === 'create-s3' ? '新增 S3 文件服务' : serviceDialog.mode === 'create-webdav' ? '新增 WebDAV 文件服务' : `配置 ${selectedServiceName}`}
          mode={serviceDialog.mode}
          target={serviceDialog.target || selectedTarget}
          runtime={configState.runtime}
          localValues={{ filePath: draft.filePath || '', urlPrefix: draft.urlPrefix || '' }}
          s3Values={serviceDialog.mode === 'create-s3' ? emptyS3 : selectedS3}
          webdavValues={serviceDialog.mode === 'create-webdav' ? emptyWebDAV : selectedWebDAV}
          onCancel={() => setServiceDialog(null)}
          onSave={serviceDialog.mode === 'create-s3' ? createS3 : serviceDialog.mode === 'create-webdav' ? createWebDAV : saveService}
          onDelete={deleteService}
        />
      )}
    </section>
  );
}

function ServiceConfigDialog({ title, mode, target, runtime, localValues, s3Values, webdavValues, onCancel, onSave, onDelete }) {
  const isCreateS3 = mode === 'create-s3';
  const isCreateWebDAV = mode === 'create-webdav';
  const isLocal = target === 'local' && !isCreateS3 && !isCreateWebDAV;
  const isWebDAV = isCreateWebDAV || target.startsWith('webdav:');
  const remoteDefaults = isWebDAV ? emptyWebDAV : emptyS3;
  const remoteValues = isWebDAV ? webdavValues : s3Values;
  const [values, setValues] = useState(isLocal ? localValues : { ...remoteDefaults, ...(remoteValues || {}) });
  const [saving, setSaving] = useState(false);
  const [testing, setTesting] = useState(false);
  const [result, setResult] = useState(null);

  function update(field, value) {
    setValues((current) => ({ ...current, [field]: value }));
  }

  async function handleSave() {
    setSaving(true);
    setResult(null);
    let failed = false;
    try {
      await onSave(values);
    } catch (err) {
      failed = true;
      setResult({ type: 'bad', message: err.message });
    } finally {
      if (failed) setSaving(false);
    }
  }

  async function handleDelete() {
    setSaving(true);
    setResult(null);
    try {
      await onDelete();
    } catch (err) {
      setResult({ type: 'bad', message: err.message });
      setSaving(false);
    }
  }

  async function testRemote() {
    setTesting(true);
    setResult(null);
    try {
      const path = isWebDAV ? '/webdav/test' : '/s3/test';
      const data = await api(path, { method: 'POST', body: JSON.stringify(values) });
      setResult({ type: 'ok', message: data.message || `${isWebDAV ? 'WebDAV' : 'S3'} 连通` });
    } catch (err) {
      setResult({ type: 'bad', message: err.message });
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
              <Field label="filePath" value={values.filePath} onChange={(value) => update('filePath', value)} />
              <Field label="urlPrefix" value={values.urlPrefix} onChange={(value) => update('urlPrefix', value)} />
            </div>
          </>
        ) : (
          <>
            <div className="form-grid">
              {Object.keys(remoteDefaults).map((field) => (
                <Field key={field} label={field} value={values[field] || ''} onChange={(value) => update(field, value)} />
              ))}
            </div>
          </>
        )}

        {result && <div className={`notice ${result.type}`}>{result.message}</div>}

        <div className="row-actions">
          {!isLocal && !isCreateS3 && !isCreateWebDAV && <button className="danger" disabled={saving} onClick={handleDelete}>{saving ? '删除中...' : '删除配置'}</button>}
          {!isLocal && <button className="ghost" disabled={testing || saving} onClick={testRemote}>{testing ? '测试中...' : '测试连通性'}</button>}
          <button className="ghost" disabled={saving} onClick={onCancel}>取消</button>
          <button disabled={saving} onClick={handleSave}>{saving ? '保存中...' : '保存'}</button>
        </div>
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
  return (
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
    </div>
  );
}

function FolderDialog({ path, onCancel, onCreate }) {
  const [name, setName] = useState('');
  const [error, setError] = useState('');
  const [saving, setSaving] = useState(false);

  async function handleSubmit(event) {
    event.preventDefault();
    const trimmed = name.trim();
    const validationError = validateFolderName(trimmed);
    if (validationError) {
      setError(validationError);
      return;
    }
    setSaving(true);
    setError('');
    try {
      await onCreate(trimmed);
    } catch (err) {
      setError(err.message);
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
        {error && <div className="notice bad">{error}</div>}
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
            {preview.error && <div className="notice bad">{preview.error}</div>}
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
  return options;
}

function selectedTargetID(targets) {
  return targets?.find((target) => target.selected)?.id || targets?.[0]?.id || 'local';
}

function serviceName(target, s3, webdav) {
  if (target === 'local') return 'Local 文件服务';
  if (target.startsWith('s3:')) {
    const index = Number(target.slice(3));
    return s3?.name || s3?.bucket || `S3 ${index + 1}`;
  }
  if (target.startsWith('webdav:')) {
    const index = Number(target.slice(7));
    return webdav?.name || webdav?.endpoint || `WebDAV ${index + 1}`;
  }
  return target;
}

function effectiveDefaultTarget(draft) {
  if (draft.defaultTarget) return draft.defaultTarget;
  const s3Index = (draft.s3 || []).findIndex((item) => item.selected);
  if (s3Index >= 0) return `s3:${s3Index}`;
  const webdavIndex = (draft.webdav || []).findIndex((item) => item.selected);
  if (webdavIndex >= 0) return `webdav:${webdavIndex}`;
  return 'local';
}

function syncDefaultTarget(draft, target) {
  const defaultTarget = target || effectiveDefaultTarget(draft);
  return {
    ...draft,
    defaultTarget,
    s3: (draft.s3 || []).map(stripSelected),
    webdav: (draft.webdav || []).map(stripSelected),
  };
}

function updateDefaultAfterDelete(previousDraft, nextDraft, deletedTarget) {
  const previousDefault = effectiveDefaultTarget(previousDraft);
  const defaultTarget = remapDefaultAfterDelete(previousDefault, deletedTarget);
  return {
    ...nextDraft,
    defaultTarget,
    s3: (nextDraft.s3 || []).map(stripSelected),
    webdav: (nextDraft.webdav || []).map(stripSelected),
  };
}

function stripSelected(item) {
  const { selected: _selected, ...rest } = item || {};
  return rest;
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
  const match = /^(s3|webdav):(\d+)$/.exec(target || '');
  if (!match) return null;
  return { type: match[1], index: Number(match[2]) };
}

function Field({ label, value, onChange, type = 'text' }) {
  return (
    <label className="field">
      <span>{label}</span>
      <input type={type} value={value ?? ''} onChange={(event) => onChange(event.target.value)} />
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
