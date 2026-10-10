import './style.css';

import {marked} from 'marked';
import DOMPurify from 'dompurify';
import {Events} from '@wailsio/runtime';
import {Launcher} from '../bindings/cmdcmd';

const stage = document.getElementById('stage');
const chat = document.getElementById('chat');
const history = document.getElementById('history');
const menu = document.getElementById('menu');
const input = document.getElementById('input');
const sendButton = document.getElementById('send');
const notice = document.getElementById('notice');

let current = null;   // assistant message being streamed into: {el, text}
let thinking = null;  // "Thinking…" placeholder element
let pending = 0;      // turns sent but not finished
let commands = [];    // slash commands from Claude Code
let menuItems = [];   // commands currently shown in the menu
let selected = 0;     // index into menuItems

// ---------- layout ----------

// The chat card grows out of the input once there is something to show:
// history, the slash command menu, or a notice. CSS animates it.
function updateLayout() {
    const expanded = history.childElementCount > 0 || !menu.hidden || !notice.hidden;
    stage.classList.toggle('expanded', expanded);
}

// ---------- history ----------

function nearBottom() {
    return history.scrollHeight - history.scrollTop - history.clientHeight < 48;
}

function add(className, build) {
    const stick = nearBottom();
    const el = document.createElement('div');
    el.className = 'msg ' + className;
    build?.(el);
    history.insertBefore(el, thinking);
    if (stick) history.scrollTop = history.scrollHeight;
    updateLayout();
    return el;
}

function addText(className, text) {
    return add(className, el => { el.textContent = text; });
}

function addMarkdown(text) {
    return add('assistant', el => { el.innerHTML = DOMPurify.sanitize(marked.parse(text)); });
}

function addTool(tool, text) {
    return add('tool', el => {
        const name = document.createElement('span');
        name.className = 'name';
        name.textContent = tool;
        const detail = document.createElement('span');
        detail.className = 'detail';
        detail.textContent = text ?? '';
        el.append(name, detail);
    });
}

function setThinking(on) {
    if (on && !thinking) {
        thinking = document.createElement('div');
        thinking.className = 'thinking';
        thinking.textContent = 'Thinking…';
        history.appendChild(thinking);
        history.scrollTop = history.scrollHeight;
    } else if (!on && thinking) {
        thinking.remove();
        thinking = null;
    }
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

// setBusy adjusts the count of turns in flight; null resets it.
function setBusy(delta) {
    pending = delta === null ? 0 : Math.max(0, pending + delta);
    sendButton.classList.toggle('busy', pending > 0);
    setThinking(pending > 0 && !current);
}

function clearHistory() {
    current = null;
    thinking = null;
    history.replaceChildren();
    setBusy(null);
    updateLayout();
    input.focus();
}

Events.On('claude', ({data: ev}) => {
    switch (ev.type) {
        case 'commands':
            commands = ev.commands ?? [];
            if (!menu.hidden) updateMenu();
            break;
        case 'block':
            current = null;
            break;
        case 'text':
            if (!current) {
                setThinking(false);
                current = {el: add('assistant'), text: ''};
            }
            current.text += ev.text;
            renderCurrent();
            break;
        case 'tool':
            current = null;
            addTool(ev.tool, ev.text);
            setThinking(true);
            break;
        case 'tool_error': {
            const tools = history.querySelectorAll('.msg.tool');
            tools[tools.length - 1]?.classList.add('failed');
            break;
        }
        case 'done':
            current = null;
            if (ev.isError) addText('system error', ev.text || 'Something went wrong.');
            setBusy(-1);
            break;
        case 'exit':
            current = null;
            setBusy(null);
            addText('system error', 'Claude stopped' + (ev.text ? ': ' + ev.text : '.') + ' Your next message starts it again.');
            break;
    }
});

// ---------- sending ----------

async function send() {
    const text = input.value.trim();
    if (!text) return;
    setInput('');
    if (text === '/clear') {
        Launcher.NewSession();
        return;
    }
    current = null;
    addText('user', text);
    history.scrollTop = history.scrollHeight;
    setBusy(+1);
    try {
        await Launcher.Send(text);
    } catch (err) {
        setBusy(-1);
        addText('system error', String(err));
    }
}

function setInput(value) {
    input.value = value;
    onInput();
}

function onInput() {
    input.style.height = 'auto';
    input.style.height = input.scrollHeight + 'px';
    sendButton.disabled = input.value.trim() === '';
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
        if (openKind) closePicker();
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
        closePicker();
        input.focus();
        const end = input.value.length;
        input.setRangeText(e.key, end, end, 'end');
        onInput();
    }
});

// ---------- agent / model / effort pickers ----------

// Three dropdowns in the tray share one popover. Choosing an option keeps
// the popover open; it closes on Esc, a click elsewhere, or its button.
const pickerPop = document.getElementById('picker-pop');
const popList = document.getElementById('pop-list');
const pickerButtons = {
    session: document.getElementById('session-button'),
    agent: document.getElementById('agent-button'),
    model: document.getElementById('model-button'),
    effort: document.getElementById('effort-button'),
};
const effortNames = {minimal: 'Minimal', low: 'Low', medium: 'Medium', high: 'High', xhigh: 'Extra high', max: 'Max', ultra: 'Ultra'};

let agents = [];
let selection = {agent: 'claude', model: '', effort: ''};
let openKind = null; // which dropdown the popover belongs to
let sessions = {current: '', sessions: []};

function currentAgent() {
    return agents.find(a => a.id === selection.agent);
}

function currentModel() {
    const models = currentAgent()?.models ?? [];
    return models.find(m => m.value === selection.model) ?? models.find(m => m.value === 'default') ?? models[0];
}

// options returns the rows for one dropdown: {label, description, active,
// disabled, pick}.
function options(kind) {
    if (kind === 'session') {
        if (sessions.sessions.length === 0) {
            return [{label: 'No past sessions', description: 'Sessions you start appear here.', disabled: true}];
        }
        return sessions.sessions.map(s => ({
            label: s.title,
            description: timeAgo(s.updated),
            active: s.id === sessions.current,
            pick: async () => {
                await Launcher.ResumeSession(s.id);
                await loadSessions();
                renderList();
            },
        }));
    }
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
    return ['', ...(currentModel()?.efforts ?? [])].map(level => ({
        label: level ? (effortNames[level] ?? level) : 'Auto',
        description: level ? '' : "The model's default",
        active: level === selection.effort,
        pick: () => choose({...selection, effort: level}),
    }));
}

function renderPicker() {
    const agent = currentAgent();
    const model = currentModel();
    const efforts = model?.efforts ?? [];

    pickerButtons.agent.querySelector('.label').textContent = agent?.name ?? 'Claude';
    pickerButtons.model.querySelector('.label').textContent = model ? model.label.replace(/\s*\(.*\)$/, '') : '';
    pickerButtons.effort.querySelector('.label').textContent =
        selection.effort ? (effortNames[selection.effort] ?? selection.effort) : 'Auto effort';
    pickerButtons.session.hidden = selection.agent === 'custom';
    pickerButtons.model.hidden = (agent?.models ?? []).length === 0;
    pickerButtons.effort.hidden = efforts.length === 0;

    if (openKind && pickerButtons[openKind].hidden) closePicker();
    if (openKind) renderList();
}

function renderList() {
    const custom = agents.find(a => a.id === 'custom');
    customCmd.hidden = openKind !== 'agent';
    if (document.activeElement !== customInput) customInput.value = custom?.command ?? '';

    popList.replaceChildren(...options(openKind).map(o => {
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
    }));
}

async function choose(next) {
    const agentChanged = next.agent !== selection.agent;
    selection = next;
    if (agentChanged) commands = [];
    renderPicker();
    try {
        await Launcher.SetSelection(next);
    } catch (err) {
        addText('system error', String(err));
    }
}

async function loadSessions() {
    sessions = await Launcher.GetSessions();
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
    renderPicker();
}

// Place the popover beside its button, on whichever side (above or below)
// has more room, and keep it inside the window.
function positionPicker() {
    if (!openKind) return;
    const gap = 10, edge = 40; // edge leaves room for the popover's shadow
    const button = pickerButtons[openKind].getBoundingClientRect();
    const width = pickerPop.offsetWidth;
    const above = button.top - gap - edge;
    const below = window.innerHeight - button.bottom - gap - edge;
    const up = above >= below;

    pickerPop.classList.toggle('up', up);
    pickerPop.classList.toggle('down', !up);
    pickerPop.style.left = Math.min(Math.max(edge, button.right - width), window.innerWidth - width - edge) + 'px';
    pickerPop.style.top = up ? 'auto' : button.bottom + gap + 'px';
    pickerPop.style.bottom = up ? window.innerHeight - button.top + gap + 'px' : 'auto';
    pickerPop.querySelector('.popover-scroll').style.maxHeight = Math.min(420, up ? above : below) + 'px';
}

async function openPicker(kind) {
    if (openKind) pickerButtons[openKind].setAttribute('aria-expanded', 'false');
    openKind = kind;
    pickerPop.dataset.kind = kind;
    pickerPop.classList.remove('closing');
    pickerButtons[kind].setAttribute('aria-expanded', 'true');
    if (kind === 'session') await loadSessions();
    renderList();
    pickerPop.hidden = false;
    positionPicker();
    await loadAgents();
    positionPicker();
}

// closePicker fades the popover out, then hides it.
function closePicker(refocus = true) {
    if (!openKind) return;
    pickerButtons[openKind].setAttribute('aria-expanded', 'false');
    openKind = null;
    saveCustomCommand();
    pickerPop.classList.add('closing');
    pickerPop.addEventListener('animationend', () => {
        if (!openKind) pickerPop.hidden = true;
        pickerPop.classList.remove('closing');
    }, {once: true});
    if (refocus) input.focus();
}

// ---------- custom agent command ----------

const customCmd = document.getElementById('custom-cmd');
const customInput = document.getElementById('custom-input');

async function saveCustomCommand() {
    const command = customInput.value.trim();
    const custom = agents.find(a => a.id === 'custom');
    if (!custom || command === (custom.command ?? '')) return;
    try {
        await Launcher.SetCustomCommand(command);
    } catch (err) {
        addText('system error', String(err));
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

for (const [kind, button] of Object.entries(pickerButtons)) {
    button.addEventListener('click', () => (openKind === kind ? closePicker() : openPicker(kind)));
}
window.addEventListener('resize', positionPicker);
Events.On('agents:changed', loadAgents);

// Clicks on the empty stage around the cards act like clicking outside the
// window: close the picker, or hide.
stage.addEventListener('mousedown', e => {
    if (openKind && !e.target.closest('.picker, #picker-pop')) {
        closePicker();
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

Events.On('session:new', clearHistory);

// A resumed session replaces the chat with its earlier messages.
Events.On('session:resumed', ({data: items}) => {
    clearHistory();
    for (const it of items ?? []) {
        if (it.role === 'user') addText('user', it.text);
        else if (it.role === 'assistant') addMarkdown(it.text);
        else addTool(it.tool, it.text);
    }
    if (!items?.length) addText('system', 'Resumed an earlier session.');
    history.scrollTop = history.scrollHeight;
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

updateLayout();
