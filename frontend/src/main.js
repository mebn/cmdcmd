import './style.css';

import {marked} from 'marked';
import DOMPurify from 'dompurify';
import {Events} from '@wailsio/runtime';
import {Launcher} from '../bindings/cmdcmd';

const history = document.getElementById('history');
const empty = document.getElementById('empty');
const input = document.getElementById('input');
const status = document.getElementById('status');
const notice = document.getElementById('notice');

let current = null; // assistant message element being streamed into
let pending = 0;    // turns sent but not finished

function setStatus(state) {
    status.className = 'dot ' + state;
    status.title = state;
}

function nearBottom() {
    return history.scrollHeight - history.scrollTop - history.clientHeight < 40;
}

function add(className, build) {
    const stick = nearBottom();
    empty.hidden = true;
    const el = document.createElement('div');
    el.className = 'msg ' + className;
    build(el);
    history.appendChild(el);
    if (stick) history.scrollTop = history.scrollHeight;
    return el;
}

function addText(className, text) {
    return add(className, el => { el.textContent = text; });
}

// Assistant text streams in token by token; re-render its markdown at most
// once per frame. Output is sanitized because the page can call Go methods.
let renderQueued = false;
function renderCurrent() {
    if (!current || renderQueued) return;
    renderQueued = true;
    requestAnimationFrame(() => {
        renderQueued = false;
        if (!current) return;
        const stick = nearBottom();
        current.el.innerHTML = DOMPurify.sanitize(marked.parse(current.text));
        if (stick) history.scrollTop = history.scrollHeight;
    });
}

Events.On('claude', ({data: ev}) => {
    switch (ev.type) {
        case 'init':
            break;
        case 'block':
            current = null;
            break;
        case 'text':
            if (!current) current = {el: add('assistant', () => {}), text: ''};
            current.text += ev.text;
            renderCurrent();
            break;
        case 'tool':
            current = null;
            add('tool', el => {
                const name = document.createElement('span');
                name.className = 'name';
                name.textContent = ev.tool;
                el.append(name, ev.text ? ' · ' + ev.text : '');
            });
            break;
        case 'tool_error': {
            const tools = history.querySelectorAll('.msg.tool');
            tools[tools.length - 1]?.classList.add('failed');
            if (ev.text) addText('system', ev.text);
            break;
        }
        case 'done':
            current = null;
            pending = Math.max(0, pending - 1);
            if (ev.isError) addText('system error', ev.text || 'Turn failed');
            setStatus(pending > 0 ? 'busy' : ev.isError ? 'error' : 'ready');
            break;
        case 'exit':
            current = null;
            pending = 0;
            setStatus('error');
            addText('system error', 'Claude exited' + (ev.text ? ': ' + ev.text : '') + '. Your next message resumes the session.');
            break;
    }
});

Events.On('window:shown', () => {
    input.focus();
    input.select();
});

Events.On('session:new', clearHistory);

function resizeInput() {
    input.style.height = 'auto';
    input.style.height = input.scrollHeight + 'px';
}

async function send() {
    const text = input.value.trim();
    if (!text) return;
    input.value = '';
    resizeInput();
    current = null;
    addText('user', text);
    history.scrollTop = history.scrollHeight;
    pending++;
    setStatus('busy');
    try {
        await Launcher.Send(text);
    } catch (err) {
        pending = Math.max(0, pending - 1);
        setStatus('error');
        addText('system error', String(err));
    }
}

function clearHistory() {
    current = null;
    pending = 0;
    history.querySelectorAll('.msg').forEach(el => el.remove());
    empty.hidden = false;
    setStatus('');
    input.focus();
}

// The Go side emits session:new, which clears the history.
function newSession() {
    Launcher.NewSession();
}

document.getElementById('new').addEventListener('click', newSession);
input.addEventListener('input', resizeInput);

document.addEventListener('keydown', e => {
    if (e.metaKey && e.key === 'n') {
        e.preventDefault();
        newSession();
    } else if (e.metaKey && e.key === 'q') {
        e.preventDefault();
        Launcher.Quit();
    } else if (e.key === 'Enter' && !e.shiftKey && !e.isComposing && e.target === input) {
        e.preventDefault();
        send();
    }
});

function showNotice(text) {
    document.getElementById('notice-text').textContent = text;
    notice.hidden = !text;
}

Events.On('notice', ({data}) => showNotice(data));
document.getElementById('notice-settings').addEventListener('click', () => Launcher.OpenPermissionSettings());

Launcher.GetInfo().then(info => {
    const home = info.cwd.replace(/^\/Users\/[^/]+|^C:\\Users\\[^\\]+/, '~');
    document.getElementById('meta').textContent = `${home} · ${info.permissionMode} · ${info.shortcut}`;
    document.getElementById('notice-settings').hidden = !info.canOpenSettings;
    showNotice(info.notice);
});
