import './style.css';

import {marked} from 'marked';
import DOMPurify from 'dompurify';
import {Events} from '@wailsio/runtime';
import {Launcher} from '../bindings/cmdcmd';

const stage = document.getElementById('stage');
const chat = document.getElementById('chat');
const menu = document.getElementById('menu');
const composer = document.getElementById('composer');
const input = document.getElementById('input');
const sendButton = document.getElementById('send');
const notice = document.getElementById('notice');

let commands = [];    // slash commands from Claude Code
let menuItems = [];   // commands currently shown in the menu
let selected = 0;     // index into menuItems

// ---------- views ----------

// Every live session has its own history pane, so one running in the
// background keeps streaming into it while another is on screen. A view is
// {key, el, current, thinking, pending}: current is the assistant message
// being streamed into ({el, text}), thinking the "Thinking…" placeholder and
// pending the number of turns sent but not finished.
const views = new Map();
let view = null; // the session on screen

function getView(key) {
    let v = views.get(key);
    if (!v) {
        const el = document.createElement('div');
        el.className = 'history';
        chat.insertBefore(el, menu);
        v = {key, el, current: null, thinking: null, pending: 0, renderQueued: false};
        views.set(key, v);
    }
    return v;
}

function showView(key) {
    view?.el.classList.remove('active');
    view = getView(key);
    view.el.classList.add('active');
    view.el.scrollTop = view.el.scrollHeight;
    sendButton.classList.toggle('busy', view.pending > 0);
    updateLayout();
}

// dropViews forgets the panes of sessions that are no longer live.
function dropViews(liveKeys) {
    for (const [key, v] of views) {
        if (v !== view && !liveKeys.has(key)) {
            v.el.remove();
            views.delete(key);
        }
    }
}

// ---------- layout ----------

// The chat card grows out of the input once there is something to show:
// history, the slash command menu, or a notice. CSS animates it.
function updateLayout() {
    const expanded = (view?.el.childElementCount ?? 0) > 0 || !menu.hidden || !notice.hidden;
    stage.classList.toggle('expanded', expanded);
}

// ---------- history ----------

function nearBottom(v) {
    return v.el.scrollHeight - v.el.scrollTop - v.el.clientHeight < 48;
}

function add(v, className, build) {
    const stick = nearBottom(v);
    const el = document.createElement('div');
    el.className = 'msg ' + className;
    build?.(el);
    v.el.insertBefore(el, v.thinking);
    if (stick) v.el.scrollTop = v.el.scrollHeight;
    if (v === view) updateLayout();
    return el;
}

function addText(v, className, text) {
    return add(v, className, el => { el.textContent = text; });
}

function addMarkdown(v, text) {
    return add(v, 'assistant', el => { el.innerHTML = DOMPurify.sanitize(marked.parse(text)); });
}

function addTool(v, tool, text) {
    return add(v, 'tool', el => {
        const name = document.createElement('span');
        name.className = 'name';
        name.textContent = tool;
        const detail = document.createElement('span');
        detail.className = 'detail';
        detail.textContent = text ?? '';
        el.append(name, detail);
    });
}

function setThinking(v, on) {
    if (on && !v.thinking) {
        v.thinking = document.createElement('div');
        v.thinking.className = 'thinking';
        v.thinking.textContent = 'Thinking…';
        v.el.appendChild(v.thinking);
        v.el.scrollTop = v.el.scrollHeight;
    } else if (!on && v.thinking) {
        v.thinking.remove();
        v.thinking = null;
    }
}

// Assistant text streams in token by token; re-render its markdown at most
// once per frame. Output is sanitized because the page can call Go methods.
function renderCurrent(v) {
    if (!v.current || v.renderQueued) return;
    v.renderQueued = true;
    requestAnimationFrame(() => {
        v.renderQueued = false;
        if (!v.current) return;
        const stick = nearBottom(v);
        v.current.el.innerHTML = DOMPurify.sanitize(marked.parse(v.current.text));
        if (stick) v.el.scrollTop = v.el.scrollHeight;
    });
}

// setBusy adjusts the count of turns in flight; null resets it.
function setBusy(v, delta) {
    v.pending = delta === null ? 0 : Math.max(0, v.pending + delta);
    if (v === view) sendButton.classList.toggle('busy', v.pending > 0);
    setThinking(v, v.pending > 0 && !v.current);
}

Events.On('claude', ({data: ev}) => {
    const v = getView(ev.session);
    switch (ev.type) {
        case 'commands':
            if (v !== view) break;
            commands = ev.commands ?? [];
            if (!menu.hidden) updateMenu();
            break;
        case 'block':
            v.current = null;
            break;
        case 'text':
            if (!v.current) {
                setThinking(v, false);
                v.current = {el: add(v, 'assistant'), text: ''};
            }
            v.current.text += ev.text;
            renderCurrent(v);
            break;
        case 'tool':
            v.current = null;
            addTool(v, ev.tool, ev.text);
            setThinking(v, true);
            break;
        case 'tool_error': {
            const tools = v.el.querySelectorAll('.msg.tool');
            tools[tools.length - 1]?.classList.add('failed');
            break;
        }
        case 'done':
            v.current = null;
            if (ev.isError) addText(v, 'system error', ev.text || 'Something went wrong.');
            setBusy(v, -1);
            break;
        case 'exit':
            v.current = null;
            setBusy(v, null);
            addText(v, 'system error', 'Claude stopped' + (ev.text ? ': ' + ev.text : '.') + ' Your next message starts it again.');
            break;
    }
});

// ---------- sending ----------

async function send() {
    const text = input.value.trim();
    if (!text || !view) return;
    setInput('');
    if (text === '/clear') {
        Launcher.NewSession();
        return;
    }
    if (text.startsWith('!')) {
        runShell(text.slice(1).trim());
        return;
    }
    const v = view;
    v.current = null;
    addText(v, 'user', text);
    v.el.scrollTop = v.el.scrollHeight;
    setBusy(v, +1);
    try {
        await Launcher.Send(text);
    } catch (err) {
        setBusy(v, -1);
        addText(v, 'system error', String(err));
    }
}

// runShell runs a ! command and shows its output. The agent sees both with
// the next message.
async function runShell(command) {
    if (!command) return;
    const v = view;
    v.current = null;
    addText(v, 'user bash', '! ' + command);
    const out = addText(v, 'shell running', 'Running…');
    v.el.scrollTop = v.el.scrollHeight;
    let res;
    try {
        res = await Launcher.RunShell(command);
    } catch (err) {
        res = {output: '', error: String(err)};
    }
    const stick = nearBottom(v);
    out.classList.remove('running');
    out.classList.toggle('failed', !!res.error);
    out.textContent = [res.output, res.error].filter(Boolean).join('\n') || '(no output)';
    if (stick) v.el.scrollTop = v.el.scrollHeight;
}

function setInput(value) {
    input.value = value;
    onInput();
}

function onInput() {
    input.style.height = 'auto';
    input.style.height = input.scrollHeight + 'px';
    sendButton.disabled = input.value.trim() === '';
    composer.classList.toggle('bash', input.value.startsWith('!'));
    updateMenu();
    updateLayout();
}

// ---------- slash command menu ----------

// The menu is open while the input is a single "/word" with no space yet.
function menuQuery() {
    const m = /^\/(\S*)$/.exec(input.value);
    return m ? m[1].toLowerCase() : null;
}

function rank(cmd, q) {
    const name = cmd.name.toLowerCase();
    if (!q || name.startsWith(q)) return 0;
    if (name.split(/[:\-_]/).some(part => part.startsWith(q))) return 1;
    if (name.includes(q)) return 2;
    if (cmd.description?.toLowerCase().includes(q)) return 3;
    return -1;
}

function updateMenu() {
    const q = menuQuery();
    if (q === null) {
        closeMenu();
        return;
    }
    menuItems = commands
        .map(cmd => ({cmd, r: rank(cmd, q)}))
        .filter(x => x.r >= 0)
        .sort((a, b) => a.r - b.r || a.cmd.name.localeCompare(b.cmd.name))
        .map(x => x.cmd);
    selected = Math.min(selected, Math.max(0, menuItems.length - 1));

    menu.replaceChildren();
    if (menuItems.length === 0) {
        const empty = document.createElement('div');
        empty.className = 'menu-empty';
        empty.textContent = commands.length > 0 ? `No commands match “/${q}”`
            : selection.agent === 'claude' ? 'Loading commands…' : 'This agent has no slash commands.';
        menu.appendChild(empty);
    }
    menuItems.forEach((cmd, i) => menu.appendChild(menuRow(cmd, i, q)));

    if (menu.hidden) {
        menu.hidden = false;
        chat.classList.add('menu-open');
    }
}

function menuRow(cmd, i, q) {
    const row = document.createElement('div');
    row.className = 'cmd' + (i === selected ? ' selected' : '');
    row.setAttribute('role', 'option');

    const head = document.createElement('div');
    head.className = 'cmd-head';
    const name = document.createElement('span');
    name.className = 'cmd-name';
    const at = q ? cmd.name.toLowerCase().indexOf(q) : -1;
    if (at >= 0) {
        const mark = document.createElement('mark');
        mark.textContent = cmd.name.slice(at, at + q.length);
        name.append('/' + cmd.name.slice(0, at), mark, cmd.name.slice(at + q.length));
    } else {
        name.textContent = '/' + cmd.name;
    }
    head.appendChild(name);
    if (cmd.argumentHint) {
        const hint = document.createElement('span');
        hint.className = 'cmd-hint';
        hint.textContent = cmd.argumentHint;
        head.appendChild(hint);
    }
    const desc = document.createElement('div');
    desc.className = 'cmd-desc';
    desc.textContent = cmd.description;

    row.append(head, desc);
    row.addEventListener('mousemove', () => select(i));
    row.addEventListener('mousedown', e => {
        e.preventDefault(); // keep focus in the input
        accept(i, true);
    });
    return row;
}

function closeMenu() {
    if (menu.hidden) return;
    menu.hidden = true;
    chat.classList.remove('menu-open');
    selected = 0;
}

function select(i) {
    if (i === selected || !menuItems[i]) return;
    menu.children[selected]?.classList.remove('selected');
    selected = i;
    const row = menu.children[selected];
    row.classList.add('selected');
    row.scrollIntoView({block: 'nearest'});
}

// accept fills in the command. With runIfComplete (Enter or click), commands
// that take no arguments run right away; otherwise the input waits for them.
function accept(i, runIfComplete) {
    const cmd = menuItems[i];
    if (!cmd) return;
    setInput('/' + cmd.name + ' ');
    if (runIfComplete && !cmd.argumentHint) send();
}

// ---------- keyboard ----------

input.addEventListener('input', onInput);

document.getElementById('composer').addEventListener('submit', e => {
    e.preventDefault();
    send();
});

document.getElementById('new').addEventListener('click', () => Launcher.NewSession());

document.addEventListener('keydown', e => {
    const menuOpen = !menu.hidden && menuItems.length > 0;

    if (e.key === 'Escape') {
        e.preventDefault();
        if (settingsOpen) closeSettings();
        else if (!menu.hidden) setInput('');
        else Launcher.Hide();
        return;
    }
    if (e.metaKey && e.key.toLowerCase() === 'n') {
        e.preventDefault();
        Launcher.NewSession();
        return;
    }
    if (e.metaKey && e.key.toLowerCase() === 'q') {
        e.preventDefault();
        Launcher.Quit();
        return;
    }
    if (menuOpen && (e.key === 'ArrowDown' || e.key === 'ArrowUp')) {
        e.preventDefault();
        const n = menuItems.length;
        select((selected + (e.key === 'ArrowDown' ? 1 : n - 1)) % n);
        return;
    }
    if (menuOpen && e.key === 'Tab') {
        e.preventDefault();
        accept(selected, false);
        return;
    }
    if (e.key === 'Tab' && !settingsOpen && !e.metaKey && !e.ctrlKey && !e.altKey) {
        e.preventDefault();
        stepSession(e.shiftKey ? -1 : 1);
        return;
    }
    if (e.key === 'Enter' && !e.shiftKey && !e.isComposing && e.target === input) {
        e.preventDefault();
        if (menuOpen) accept(selected, true);
        else send();
        return;
    }

    // Typing anywhere else goes to the input (but not into another field).
    const printable = e.key.length === 1 && !e.metaKey && !e.ctrlKey && !e.altKey;
    const inField = document.activeElement?.matches('input, textarea');
    if (printable && !inField) {
        e.preventDefault();
        closeSettings();
        input.focus();
        const end = input.value.length;
        input.setRangeText(e.key, end, end, 'end');
        onInput();
    }
});

// ---------- settings: agent, model, effort ----------

// The gear in the tray opens one popover with the agent, model and effort
// choices. Choosing an option keeps it open; it closes on Esc, a click
// elsewhere, or the gear.
const settingsPop = document.getElementById('settings-pop');
const settingsButton = document.getElementById('settings-button');
const settingsLists = {
    agent: document.getElementById('agent-list'),
    model: document.getElementById('model-list'),
    effort: document.getElementById('effort-list'),
};
const effortNames = {minimal: 'Minimal', low: 'Low', medium: 'Medium', high: 'High', xhigh: 'Extra high', max: 'Max', ultra: 'Ultra'};

let agents = [];
let selection = {agent: 'claude', model: '', effort: ''};
let settingsOpen = false;

function currentAgent() {
    return agents.find(a => a.id === selection.agent);
}

function currentModel() {
    const models = currentAgent()?.models ?? [];
    return models.find(m => m.value === selection.model) ?? models.find(m => m.value === 'default') ?? models[0];
}

// options returns the rows for one section: {label, description, active,
// disabled, pick}.
function options(kind) {
    if (kind === 'agent') {
        return agents.map(a => ({
            label: a.name,
            description: a.available ? (a.command ?? '') : a.reason,
            active: a.id === selection.agent,
            disabled: !a.available,
            pick: () => choose({agent: a.id, model: '', effort: ''}),
        }));
    }
    if (kind === 'model') {
        const model = currentModel();
        return (currentAgent()?.models ?? []).map(m => ({
            label: m.label,
            description: m.description,
            active: m === model,
            pick: () => {
                // Keep the effort only if the new model supports it.
                const effort = m.efforts?.includes(selection.effort) ? selection.effort : '';
                choose({...selection, model: m.value === 'default' ? '' : m.value, effort});
            },
        }));
    }
    const efforts = currentModel()?.efforts ?? [];
    if (efforts.length === 0) return [];
    return ['', ...efforts].map(level => ({
        label: level ? (effortNames[level] ?? level) : 'Auto',
        description: level ? '' : "The model's default",
        active: level === selection.effort,
        pick: () => choose({...selection, effort: level}),
    }));
}

// renderSettings updates the gear's tooltip and, while open, the popover.
function renderSettings() {
    const model = currentModel();
    settingsButton.title = [
        currentAgent()?.name ?? 'Claude',
        model?.label.replace(/\s*\(.*\)$/, ''),
        selection.effort ? (effortNames[selection.effort] ?? selection.effort) + ' effort' : '',
    ].filter(Boolean).join(' · ');
    if (!settingsOpen) return;

    const custom = agents.find(a => a.id === 'custom');
    if (document.activeElement !== customInput) customInput.value = custom?.command ?? '';
    for (const [kind, list] of Object.entries(settingsLists)) {
        const rows = options(kind);
        list.parentElement.hidden = rows.length === 0;
        list.replaceChildren(...rows.map(optionButton));
    }
}

function optionButton(o) {
    const b = document.createElement('button');
    b.type = 'button';
    b.className = 'option' + (o.active ? ' active' : '');
    b.disabled = !!o.disabled;
    const name = document.createElement('span');
    name.className = 'option-name';
    name.textContent = o.label;
    const check = document.createElementNS('http://www.w3.org/2000/svg', 'svg');
    check.setAttribute('viewBox', '0 0 24 24');
    check.classList.add('check');
    check.innerHTML = '<path d="M5 12.5l4.5 4.5L19 7.5"/>';
    b.append(name, check);
    if (o.description) {
        const desc = document.createElement('span');
        desc.className = 'option-desc';
        desc.textContent = o.description;
        b.appendChild(desc);
    }
    b.addEventListener('click', o.pick);
    return b;
}

async function choose(next) {
    const agentChanged = next.agent !== selection.agent;
    selection = next;
    if (agentChanged) commands = [];
    renderSettings();
    try {
        await Launcher.SetSelection(next);
    } catch (err) {
        if (view) addText(view, 'system error', String(err));
    }
}

function timeAgo(iso) {
    const s = (Date.now() - new Date(iso).getTime()) / 1000;
    if (s < 60) return 'just now';
    if (s < 3600) return `${Math.floor(s / 60)} min ago`;
    if (s < 86400) return `${Math.floor(s / 3600)} h ago`;
    if (s < 7 * 86400) return `${Math.floor(s / 86400)} d ago`;
    return new Date(iso).toLocaleDateString();
}

async function loadAgents() {
    [agents, selection] = await Promise.all([Launcher.GetAgents(), Launcher.GetSelection()]);
    renderSettings();
}

// Place the popover beside the gear, on whichever side (above or below) has
// more room, and keep it inside the window.
function positionSettings() {
    if (!settingsOpen) return;
    const gap = 10, edge = 40; // edge leaves room for the popover's shadow
    const button = settingsButton.getBoundingClientRect();
    const width = settingsPop.offsetWidth;
    const above = button.top - gap - edge;
    const below = window.innerHeight - button.bottom - gap - edge;
    const up = above >= below;

    settingsPop.classList.toggle('up', up);
    settingsPop.classList.toggle('down', !up);
    settingsPop.style.left = Math.min(Math.max(edge, button.right - width), window.innerWidth - width - edge) + 'px';
    settingsPop.style.top = up ? 'auto' : button.bottom + gap + 'px';
    settingsPop.style.bottom = up ? window.innerHeight - button.top + gap + 'px' : 'auto';
    settingsPop.querySelector('.popover-scroll').style.maxHeight = Math.min(520, up ? above : below) + 'px';
}

async function openSettings() {
    settingsOpen = true;
    settingsPop.classList.remove('closing');
    settingsButton.setAttribute('aria-expanded', 'true');
    renderSettings();
    settingsPop.hidden = false;
    positionSettings();
    await loadAgents();
    positionSettings();
}

// closeSettings fades the popover out, then hides it.
function closeSettings(refocus = true) {
    if (!settingsOpen) return;
    settingsOpen = false;
    settingsButton.setAttribute('aria-expanded', 'false');
    saveCustomCommand();
    settingsPop.classList.add('closing');
    settingsPop.addEventListener('animationend', () => {
        if (!settingsOpen) settingsPop.hidden = true;
        settingsPop.classList.remove('closing');
    }, {once: true});
    if (refocus) input.focus();
}

// ---------- custom agent command ----------

const customInput = document.getElementById('custom-input');

async function saveCustomCommand() {
    const command = customInput.value.trim();
    const custom = agents.find(a => a.id === 'custom');
    if (!custom || command === (custom.command ?? '')) return;
    try {
        await Launcher.SetCustomCommand(command);
    } catch (err) {
        if (view) addText(view, 'system error', String(err));
    }
    await loadAgents();
}

customInput.addEventListener('keydown', async e => {
    if (e.key !== 'Enter') return;
    e.preventDefault();
    e.stopPropagation();
    await saveCustomCommand();
    if (customInput.value.trim() && selection.agent !== 'custom') {
        choose({agent: 'custom', model: '', effort: ''});
    }
});
customInput.addEventListener('blur', saveCustomCommand);

settingsButton.addEventListener('click', () => (settingsOpen ? closeSettings() : openSettings()));
window.addEventListener('resize', positionSettings);
Events.On('agents:changed', loadAgents);

// Clicks on the empty stage around the cards act like clicking outside the
// window: close the settings, or hide.
stage.addEventListener('mousedown', e => {
    if (settingsOpen && !e.target.closest('#settings-button, #settings-pop')) {
        closeSettings();
        return;
    }
    // The window card's padding is invisible while collapsed, so it counts as
    // outside too.
    const collapsedPadding = e.target.id === 'window' && !stage.classList.contains('expanded');
    if (e.target === stage || e.target.id === 'panel' || collapsedPadding) {
        Launcher.Hide();
    }
});

// ---------- window + notices ----------

Events.On('window:shown', () => {
    input.focus();
    input.select();
});

// ---------- sessions ----------

// The tray shows every session in a row that scrolls sideways, between the
// New and settings buttons. The symbol before each name shows its status;
// Tab and Shift+Tab step through them.
const sessionRow = document.getElementById('session-row');
const statusNames = {
    idle: 'Idle',
    running: 'Running',
    waiting: 'Finished, waiting for you',
    error: 'Stopped with an error',
};

let sessions = {current: '', sessions: []};
let activeIndex = -1; // the session on screen, or the one Tab last stepped to

async function loadSessions() {
    sessions = await Launcher.GetSessions();
    dropViews(new Set(sessions.sessions.map(s => s.key).filter(Boolean)));
    activeIndex = sessions.sessions.findIndex(s => s.key === sessions.current);
    renderSessions();
}

function renderSessions() {
    sessionRow.replaceChildren(...sessions.sessions.map((s, i) => {
        const b = document.createElement('button');
        b.type = 'button';
        b.className = 'session' + (i === activeIndex ? ' active' : '');
        b.dataset.status = s.status;
        b.title = `${s.title || 'New session'}\n${statusNames[s.status] ?? ''} · ${timeAgo(s.updated)}`;
        const status = document.createElement('span');
        status.className = 'status';
        const name = document.createElement('span');
        name.className = 'session-name';
        name.textContent = s.title || 'New session';
        b.append(status, name);
        b.addEventListener('click', () => openSession(i));
        return b;
    }));
    if (sessions.sessions.length === 0) {
        const empty = document.createElement('span');
        empty.className = 'session-empty';
        empty.textContent = 'No sessions yet';
        sessionRow.appendChild(empty);
    }
    sessionRow.children[activeIndex]?.scrollIntoView({inline: 'nearest', block: 'nearest'});
}

function openSession(i) {
    const s = sessions.sessions[i];
    if (!s) return;
    activeIndex = i;
    for (const [j, el] of [...sessionRow.children].entries()) el.classList.toggle('active', j === i);
    sessionRow.children[i]?.scrollIntoView({inline: 'nearest', block: 'nearest'});
    Launcher.OpenSession(s);
    input.focus();
}

function stepSession(dir) {
    const n = sessions.sessions.length;
    if (n === 0) return;
    openSession(activeIndex < 0 ? 0 : (activeIndex + dir + n) % n);
}

// A vertical wheel scrolls the row sideways.
sessionRow.addEventListener('wheel', e => {
    if (Math.abs(e.deltaY) <= Math.abs(e.deltaX)) return;
    e.preventDefault();
    sessionRow.scrollLeft += e.deltaY;
}, {passive: false});

Events.On('sessions:changed', loadSessions);

// A session came on screen: a new one, a live one we already hold, or one
// resumed from disk with its earlier messages.
Events.On('session:open', ({data}) => {
    const fresh = !views.has(data.key);
    const v = getView(data.key);
    if (fresh && data.history) {
        for (const it of data.history) {
            if (it.role === 'user') addText(v, 'user', it.text);
            else if (it.role === 'assistant') addMarkdown(v, it.text);
            else addTool(v, it.tool, it.text);
        }
        if (!data.history.length) addText(v, 'system', 'Resumed an earlier session.');
    }
    showView(data.key);
    input.focus();
});

function showNotice(text) {
    document.getElementById('notice-text').textContent = text;
    notice.hidden = !text;
    updateLayout();
}

Events.On('notice', ({data}) => showNotice(data));
document.getElementById('notice-settings').addEventListener('click', () => Launcher.OpenPermissionSettings());

Launcher.GetInfo().then(info => {
    document.getElementById('notice-settings').hidden = !info.canOpenSettings;
    showNotice(info.notice);
});

Launcher.GetCommands().then(list => {
    if (list?.length) commands = list;
});

loadAgents();
loadSessions().then(() => showView(sessions.current));

updateLayout();
