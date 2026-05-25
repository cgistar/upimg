import React, { useEffect, useRef, useState } from 'react';
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
  s3: [],
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
  selected: false,
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

  async function loadConfig() {
    const data = await api('/config');
    setConfigState(data);
    if (!data.targets.some((target) => target.id === selectedTarget)) {
      setSelectedTarget(data.targets[0]?.id || 'local');
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
          <button className="ghost" onClick={() => setView(view === 'config' ? 'files' : 'config')}>
            {view === 'config' ? '返回文件' : '配置'}
          </button>
          <button className="ghost" onClick={logout}>退出</button>
        </div>
      </header>

      {message && <div className="notice ok">{message}</div>}
      {error && <div className="notice bad">{error}</div>}
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
      {!session.keyConfigured && <div className="notice warn">当前未配置 key，管理界面处于免登录状态。</div>}
      {(configState.runtime.keyOverridden || configState.runtime.portOverridden || configState.runtime.basePathOverridden || configState.runtime.filePathOverridden) && (
        <div className="notice warn">检测到环境变量覆盖，保存 config.json 不会覆盖对应运行时值。</div>
      )}

      {view === 'files' ? (
        <FilePanel
          targets={configState.targets}
          selectedTarget={selectedTarget}
          setSelectedTarget={setSelectedTarget}
          path={path}
          setPath={setPath}
          objects={objects}
          busy={busy}
          onRefresh={() => loadObjects(selectedTarget, path)}
          onUpload={async (files) => {
            const form = new FormData();
            for (const file of files) form.append('file', file);
            const params = new URLSearchParams({ target: selectedTarget, path });
            await api(`/storage/upload?${params}`, { method: 'POST', body: form });
            setMessage('上传完成');
            await loadObjects(selectedTarget, path);
          }}
          onDelete={async (item) => {
            const params = new URLSearchParams({ target: selectedTarget, path: item.path });
            await api(`/storage/object?${params}`, { method: 'DELETE' });
            setMessage('删除完成');
            await loadObjects(selectedTarget, path);
          }}
          onBatchDelete={async (paths) => {
            const params = new URLSearchParams({ target: selectedTarget });
            try {
              await api(`/storage/objects?${params}`, { method: 'DELETE', body: JSON.stringify({ paths }) });
              setMessage(`已删除 ${paths.length} 个文件`);
            } finally {
              await loadObjects(selectedTarget, path);
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
              setSelectedTarget(data.targets[0]?.id || 'local');
              setPath('');
            }
            if (!targetOptions(data.config).some((target) => target.id === configTarget)) {
              setConfigTarget('local');
            }
            setMessage('配置已保存并热更新；host/port 如有变更需重启服务。');
          }}
          onError={setError}
          onConfirm={setConfirmState}
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

function FilePanel({ targets, selectedTarget, setSelectedTarget, path, setPath, objects, busy, onRefresh, onUpload, onDelete, onBatchDelete, onError, onConfirm }) {
  const [browserMode, setBrowserMode] = useState('list');
  const [preview, setPreview] = useState(null);
  const [selectedPaths, setSelectedPaths] = useState([]);
  const crumbs = path ? path.split('/').filter(Boolean) : [];
  const parentPath = crumbs.slice(0, -1).join('/');
  const entries = [
    { path: '.', displayName: '.', isDir: true, special: 'current' },
    ...(path ? [{ path: '..', displayName: '..', isDir: true, special: 'parent' }] : []),
    ...objects.map((item) => ({ ...item, displayName: basename(item.path) })),
  ];
  const selectableEntries = entries.filter((item) => !item.isDir && !item.special);
  const selectedSet = new Set(selectedPaths);
  const allSelected = selectableEntries.length > 0 && selectableEntries.every((item) => selectedSet.has(item.path));

  useEffect(() => {
    setSelectedPaths([]);
  }, [selectedTarget, path, objects]);

  async function openEntry(item) {
    if (item.special === 'current') return;
    if (item.special === 'parent') {
      setPath(parentPath);
      return;
    }
    if (item.isDir) {
      setPath(item.path.replace(/\/$/, ''));
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

  function confirmDelete(item) {
    onConfirm({
      title: '删除文件',
      message: `确认删除 ${item.path}？此操作不可撤销。`,
      confirmText: '删除',
      action: () => onDelete(item),
    });
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
      title: '批量删除文件',
      message: `确认删除已选择的 ${selectedPaths.length} 个文件？此操作会一次提交到后台且不可撤销。`,
      confirmText: '批量删除',
      action: () => onBatchDelete(selectedPaths),
    });
  }

  return (
    <section className="panel file-panel">
      <div className="nas-browser">
        <div className="nas-toolbar">
          <div className="nas-target">
            <span>文件服务</span>
            <select value={selectedTarget} onChange={(event) => {
              setSelectedTarget(event.target.value);
              setPath('');
            }}>
              {targets.map((target) => (
                <option key={target.id} value={target.id}>{target.name} · {target.type}</option>
              ))}
            </select>
          </div>
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
            <button className="ghost" onClick={onRefresh}>{busy ? '刷新中...' : '刷新'}</button>
            <button className="danger" disabled={selectedPaths.length === 0} onClick={confirmBatchDelete}>删除所选</button>
          </div>
        </div>

        <div className="nas-pathbar">
          <span className="path-label">路径</span>
          <button onClick={() => setPath('')}>/</button>
          {crumbs.map((part, index) => (
            <button key={`${part}-${index}`} onClick={() => setPath(crumbs.slice(0, index + 1).join('/'))}>{part}</button>
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
              <span>操作</span>
            </div>
            {entries.map((item) => (
              <div className={item.isDir ? 'file-row dir' : 'file-row'} role="row" key={`${item.special || 'file'}:${item.path}`}>
                <span className="file-cell check" role="cell">
                  <input
                    type="checkbox"
                    checked={selectedSet.has(item.path)}
                    disabled={item.isDir || item.special}
                    onChange={() => togglePath(item.path)}
                    onClick={(event) => event.stopPropagation()}
                  />
                </span>
                <button className="file-cell name" role="cell" onClick={() => openEntry(item)}>
                  <span>{item.displayName}</span>
                </button>
                <span className="file-cell size" role="cell">{item.isDir ? '-' : formatSize(item.size)}</span>
                <span className="file-cell time" role="cell">{item.modTime ? formatDateTime(item.modTime) : '-'}</span>
                <span className="file-cell actions" role="cell">
                  <button className="danger" disabled={item.isDir || item.special} onClick={() => confirmDelete(item)}>删除</button>
                </span>
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
                    disabled={item.isDir || item.special}
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
                <button className="danger" disabled={item.isDir || item.special} onClick={() => confirmDelete(item)}>删除</button>
              </div>
            ))}
          </div>
        )}
      </div>
      {preview && <PreviewDialog preview={preview} onClose={() => setPreview(null)} />}
    </section>
  );
}

function ConfigPanel({ configState, selectedTarget, setSelectedTarget, onSaved, onError, onConfirm }) {
  const [draft, setDraft] = useState(configState.config);
  const [testing, setTesting] = useState(null);
  const [testResult, setTestResult] = useState(null);

  useEffect(() => setDraft(configState.config), [configState]);

  const s3Index = selectedTarget.startsWith('s3:') ? Number(selectedTarget.slice(3)) : -1;
  const selectedS3 = s3Index >= 0 ? draft.s3?.[s3Index] : null;

  function update(field, value) {
    setDraft((current) => ({ ...current, [field]: value }));
  }

  function updateS3(index, field, value) {
    setDraft((current) => ({
      ...current,
      s3: current.s3.map((item, itemIndex) => {
        if (itemIndex !== index) return item;
        return { ...item, [field]: value };
      }),
    }));
  }

  async function save() {
    try {
      const previousBasePath = normalizeBasePath(configState.runtime.basePath || '');
      const data = await api('/config', { method: 'PUT', body: JSON.stringify(draft) });
      onSaved(data);
      const nextBasePath = normalizeBasePath(data.runtime.basePath || '');
      if (previousBasePath !== nextBasePath) {
        window.location.assign(`${nextBasePath}/admin/`);
      }
    } catch (err) {
      onError(err.message);
    }
  }

  async function testS3(index) {
    setTesting(index);
    setTestResult(null);
    try {
      const data = await api('/s3/test', { method: 'POST', body: JSON.stringify(draft.s3[index]) });
      setTestResult({ type: 'ok', message: data.message || 'S3 连通' });
    } catch (err) {
      setTestResult({ type: 'bad', message: err.message });
    } finally {
      setTesting(null);
    }
  }

  return (
    <section className="panel config-panel">
      <div className="panel-head">
        <div>
          <p className="eyebrow">config</p>
          <h2>配置管理</h2>
        </div>
        <button onClick={save}>保存并热更新</button>
      </div>

      <div className="config-groups">
        <section className="config-group">
          <div>
            <p className="eyebrow">common</p>
            <h3>公共配置</h3>
            <p className="muted">这些字段影响服务鉴权、监听地址和上传命名规则。</p>
          </div>
          <div className="form-grid">
            <Field label="key" value={draft.key} onChange={(value) => update('key', value)} />
            <Field label="rename" value={draft.rename} onChange={(value) => update('rename', value)} />
            <Field label="host" value={draft.host} onChange={(value) => update('host', value)} />
            <Field label="port" type="number" value={draft.port || ''} onChange={(value) => update('port', Number(value) || 0)} />
            <Field label="basePath" value={draft.basePath} onChange={(value) => update('basePath', value)} />
          </div>
        </section>

        <section className="config-group">
          <div className="config-group-head">
            <div>
              <p className="eyebrow">storage</p>
              <h3>文件服务配置</h3>
              <p className="muted">选择与文件浏览相同的目标，只编辑当前目标对应的配置。</p>
            </div>
            <button className="ghost" onClick={() => {
              const next = [...(draft.s3 || []), { ...emptyS3 }];
              update('s3', next);
              setSelectedTarget(`s3:${next.length - 1}`);
            }}>新增 S3</button>
          </div>

          <div className="toolbar">
            <select value={selectedTarget} onChange={(event) => setSelectedTarget(event.target.value)}>
              {targetOptions(draft).map((target) => (
                <option key={target.id} value={target.id}>{target.name} · {target.type}</option>
              ))}
            </select>
          </div>

          {selectedTarget === 'local' && (
            <div className="service-card">
              <div className="s3-title">
                <strong>Local 文件服务</strong>
                <span className="muted">当前根目录：{configState.runtime.localRoot}</span>
              </div>
              <div className="form-grid">
                <Field label="filePath" value={draft.filePath} onChange={(value) => update('filePath', value)} />
                <Field label="urlPrefix" value={draft.urlPrefix} onChange={(value) => update('urlPrefix', value)} />
              </div>
            </div>
          )}

          {selectedTarget !== 'local' && selectedS3 && (
            <div className="service-card">
              <div className="s3-title">
                <strong>{selectedS3.name || selectedS3.bucket || `S3 ${s3Index + 1}`}</strong>
                <label>
                  <input type="checkbox" checked={Boolean(selectedS3.selected)} onChange={(event) => {
                    const checked = event.target.checked;
                    setDraft((current) => ({
                      ...current,
                      s3: current.s3.map((entry, entryIndex) => ({ ...entry, selected: entryIndex === s3Index ? checked : false })),
                    }));
                  }} />
                  设为默认
                </label>
              </div>
              <div className="form-grid">
                {Object.keys(emptyS3).filter((field) => field !== 'selected').map((field) => (
                  <Field key={field} label={field} value={selectedS3[field] || ''} onChange={(value) => updateS3(s3Index, field, value)} />
                ))}
              </div>
              <div className="row-actions">
                <button className="ghost" disabled={testing === s3Index} onClick={() => testS3(s3Index)}>{testing === s3Index ? '测试中...' : '测试连通性'}</button>
                <button className="danger" onClick={() => {
                  const name = selectedS3.name || selectedS3.bucket || `S3 ${s3Index + 1}`;
                  onConfirm({
                    title: '删除 S3 配置',
                    message: `确认删除 ${name}？保存配置前仍可刷新页面放弃本次改动。`,
                    confirmText: '删除配置',
                    action: async () => {
                      update('s3', draft.s3.filter((_, itemIndex) => itemIndex !== s3Index));
                      setSelectedTarget('local');
                      setTestResult(null);
                    },
                  });
                }}>删除配置</button>
              </div>
              {testResult && <div className={`notice ${testResult.type}`}>{testResult.message}</div>}
            </div>
          )}
        </section>
      </div>
    </section>
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
  return options;
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

createRoot(document.getElementById('root')).render(<App />);
