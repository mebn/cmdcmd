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
let attached = [];    // files to send with the next message: {path, name, preview}

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

// While a turn runs, "Thinking…" stays below its output, from the message
// being sent until the turn finishes, and shows how long it has been going.
function elapsed(ms) {
    const s = Math.max(0, Math.floor(ms / 1000));
    return s < 60 ? `${s}s` : `${Math.floor(s / 60)}m ${String(s % 60).padStart(2, '0')}s`;
}

function tickThinking(v) {
    if (v.thinking && v.turnStart) v.thinking.lastChild.textContent = elapsed(Date.now() - v.turnStart);
}

setInterval(() => views.forEach(tickThinking), 1000);

function setThinking(v, on) {
    if (on && !v.thinking) {
        v.thinking = document.createElement('div');
        v.thinking.className = 'thinking';
        const label = document.createElement('span');
        label.className = 'thinking-label';
        label.textContent = 'Thinking…';
        const time = document.createElement('span');
        time.className = 'thinking-time';
        v.thinking.append(label, time);
        tickThinking(v);
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
    const before = v.pending;
    v.pending = delta === null ? 0 : Math.max(0, v.pending + delta);
    if (before === 0 && v.pending > 0) v.turnStart = Date.now();
    if (v.pending === 0) {
        v.turnStart = null;
        v.outside = false;
    }
    if (v === view) sendButton.classList.toggle('busy', v.pending > 0);
    setThinking(v, v.pending > 0);
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
            if (!v.current) v.current = {el: add(v, 'assistant'), text: ''};
            v.current.text += ev.text;
            renderCurrent(v);
            break;
        case 'tool':
            v.current = null;
            addTool(v, ev.tool, ev.text);
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
    if ((!text && !attached.length) || !view) return;
    if (text === '/clear') {
        setInput('');
        Launcher.NewSession();
        return;
    }
    if (text.startsWith('!')) {
        setInput('');
        runShell(text.slice(1).trim());
        return;
    }
    const files = attached;
    attached = [];
    renderAttachments();
    setInput('');
    const v = view;
    v.current = null;
    addUser(v, text, files);
    v.el.scrollTop = v.el.scrollHeight;
    setBusy(v, +1);
    try {
        await Launcher.Send(text, files.map(a => a.path));
    } catch (err) {
        setBusy(v, -1);
        addText(v, 'system error', String(err));
    }
}

// addUser shows a sent message, with thumbnails or names of its attachments.
function addUser(v, text, files) {
    return add(v, 'user', el => {
        if (files.length) {
            const row = document.createElement('div');
            row.className = 'msg-attachments';
            row.append(...files.map(a => {
                if (a.preview) {
                    const img = document.createElement('img');
                    img.src = a.preview;
                    img.alt = a.name;
                    return img;
                }
                const name = document.createElement('span');
                name.className = 'file-name';
                name.textContent = '📎 ' + a.name;
                return name;
            }));
            el.append(row);
        }
        if (text) el.append(text);
    });
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
    sendButton.disabled = input.value.trim() === '' && attached.length === 0;
    composer.classList.toggle('bash', input.value.startsWith('!'));
    updateMenu();
    updateLayout();
}

// ---------- attachments ----------

// Files and images go with the next message. They are picked with the +
// button or pasted: copied files by path, anything else (a screenshot, an
// image copied from a page) saved to a temporary file first.
const attachButton = document.getElementById('attach');
const attachmentRow = document.getElementById('attachments');

const fileIcon = '<svg viewBox="0 0 24 24" aria-hidden="true"><path d="M14 3H7a2 2 0 0 0-2 2v14a2 2 0 0 0 2 2h10a2 2 0 0 0 2-2V8z"/><path d="M14 3v5h5"/></svg>';
const removeIcon = '<svg viewBox="0 0 24 24" aria-hidden="true"><path d="M6 6l12 12M18 6L6 18"/></svg>';

function attach(list) {
    if (!list?.length) return;
    for (const a of list) {
        if (!attached.some(b => b.path === a.path)) attached.push(a);
    }
    renderAttachments();
    input.focus();
}

// Chips are added and removed one by one, so only those that change animate.
const attachmentTrack = attachmentRow.querySelector('.attachments-track');
const chips = new Map(); // path → chip element

function renderAttachments() {
    for (const a of attached) {
        if (chips.has(a.path)) continue;
        const el = attachmentChip(a);
        chips.set(a.path, el);
        attachmentTrack.append(el);
        el.scrollIntoView({block: 'nearest', inline: 'nearest', behavior: 'smooth'});
    }
    for (const [path, el] of chips) {
        if (attached.some(a => a.path === path)) continue;
        chips.delete(path);
        removeChip(el);
    }
    attachmentRow.classList.toggle('open', attached.length > 0);
    onInput();
}

function attachmentChip(a) {
    const el = document.createElement('div');
    el.className = 'attachment' + (a.preview ? ' image' : '');
    el.dataset.tip = a.path;
    if (a.preview) {
        const img = document.createElement('img');
        img.src = a.preview;
        img.alt = a.name;
        el.append(img);
    } else {
        const icon = document.createElement('span');
        icon.className = 'file-icon';
        icon.innerHTML = fileIcon;
        const name = document.createElement('span');
        name.className = 'file-name';
        name.textContent = a.name;
        el.append(icon, name);
    }
    const remove = document.createElement('button');
    remove.type = 'button';
    remove.className = 'remove';
    remove.setAttribute('aria-label', 'Remove ' + a.name);
    remove.innerHTML = removeIcon;
    remove.addEventListener('click', () => {
        attached = attached.filter(b => b !== a);
        renderAttachments();
        input.focus();
    });
    el.append(remove);
    return el;
}

// removeChip shrinks a chip away from its current width, then drops it.
function removeChip(el) {
    el.style.width = el.offsetWidth + 'px';
    void el.offsetWidth; // start the transition from that width
    el.classList.add('leaving');
    el.style.width = '0px';
    el.addEventListener('transitionend', e => {
        if (e.target === el && e.propertyName === 'width') el.remove();
    });
    setTimeout(() => el.remove(), 400); // in case no transition runs
}

function showError(err) {
    if (view) addText(view, 'system error', String(err));
}

attachButton.addEventListener('click', async () => {
    try {
        attach(await Launcher.ChooseAttachments());
    } catch (err) {
        showError(err);
    }
});

function readBase64(file) {
    return new Promise((resolve, reject) => {
        const reader = new FileReader();
        reader.onload = () => resolve(String(reader.result).replace(/^[^,]*,/, ''));
        reader.onerror = () => reject(reader.error);
        reader.readAsDataURL(file);
    });
}

async function pasteFiles(files) {
    try {
        // Files copied in Finder are attached where they are.
        const copied = await Launcher.ClipboardFiles();
        if (copied?.length) {
            attach(copied);
            return;
        }
        attach(await Promise.all(files.map(async f =>
            Launcher.SaveAttachment(f.name || 'pasted', await readBase64(f)))));
    } catch (err) {
        showError(err);
    }
}

document.addEventListener('paste', e => {
    const files = [...(e.clipboardData?.files ?? [])];
    if (!files.length) return;
    e.preventDefault();
    pasteFiles(files);
});

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

// ⌘ on macOS, Ctrl elsewhere.
const isMac = /Mac/.test(navigator.platform);
const hasMod = e => (isMac ? e.metaKey : e.ctrlKey);

for (const kbd of document.querySelectorAll('kbd[data-keys]')) {
    kbd.textContent = kbd.dataset.keys.split('+').map(k => ({
        mod: isMac ? '⌘' : 'Ctrl+',
        shift: isMac ? '⇧' : 'Shift+',
    })[k] ?? k).join('');
}

input.addEventListener('input', onInput);

document.getElementById('composer').addEventListener('submit', e => {
    e.preventDefault();
    send();
});

document.addEventListener('keydown', e => {
    const menuOpen = !menu.hidden && menuItems.length > 0;

    if (e.key === 'Escape') {
        e.preventDefault();
        if (openPop) closePop();
        else if (!menu.hidden) setInput('');
        else Launcher.Hide();
        return;
    }
    if (hasMod(e) && !e.altKey && e.key.toLowerCase() === 'n') {
        e.preventDefault();
        closePop();
        if (e.shiftKey) newFolder();
        else newSession();
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
    // Tab and Shift+Tab step through the sessions, never the folders.
    if (e.key === 'Tab' && !openPop && !e.metaKey && !e.ctrlKey && !e.altKey) {
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
        closePop();
        input.focus();
        const end = input.value.length;
        input.setRangeText(e.key, end, end, 'end');
        onInput();
    }
});

// ---------- popovers ----------

// One popover is open at a time: settings, the New menu or a context menu.
// Each is placed next to its anchor on whichever side (above or below) has
// more room, and kept inside the window. It closes on Esc, a click
// elsewhere, or its button.
let openPop = null; // {el, button}

// anchor is a DOMRect; align is 'left' or 'right' (which anchor edge the
// popover lines up with).
function placePop(el, anchor, align) {
    const gap = 8, edge = 40; // edge leaves room for the popover's shadow
    const width = el.offsetWidth;
    const above = anchor.top - gap - edge;
    const below = window.innerHeight - anchor.bottom - gap - edge;
    const up = above >= below;
    const x = align === 'right' ? anchor.right - width : anchor.left;

    el.classList.toggle('up', up);
    el.classList.toggle('down', !up);
    el.classList.toggle('from-left', align === 'left');
    el.style.left = Math.min(Math.max(edge, x), window.innerWidth - width - edge) + 'px';
    el.style.top = up ? 'auto' : anchor.bottom + gap + 'px';
    el.style.bottom = up ? window.innerHeight - anchor.top + gap + 'px' : 'auto';
    el.querySelector('.popover-scroll').style.maxHeight = Math.min(520, up ? above : below) + 'px';
}

function showPop(el, anchor, align, button = null) {
    if (openPop?.el !== el) closePop(false);
    openPop = {el, button, anchor, align};
    hideTip();
    el.classList.remove('closing');
    button?.setAttribute('aria-expanded', 'true');
    el.hidden = false;
    placePop(el, anchor, align);
}

function repositionPop() {
    if (openPop) placePop(openPop.el, openPop.button?.getBoundingClientRect() ?? openPop.anchor, openPop.align);
}

// closePop fades the open popover out, then hides it.
function closePop(refocus = true) {
    if (!openPop) return;
    const {el, button} = openPop;
    openPop = null;
    button?.setAttribute('aria-expanded', 'false');
    el.classList.add('closing');
    el.addEventListener('animationend', () => {
        if (openPop?.el !== el) el.hidden = true;
        el.classList.remove('closing');
    }, {once: true});
    if (refocus) input.focus();
}

function togglePop(el, button, align, before) {
    if (openPop?.el === el) {
        closePop();
        return;
    }
    before?.();
    showPop(el, button.getBoundingClientRect(), align, button);
}

window.addEventListener('resize', repositionPop);

// ---------- tooltip ----------

// Hovering a button, folder or session shows a tooltip after a short delay:
// a [data-tip] attribute, or lines from tipLines for folders and sessions.
const tip = document.getElementById('tip');
let tipTarget = null;
let tipTimer = 0;

function tipFor(el) {
    if (el.dataset.tip) {
        const keys = el.dataset.tipKeys;
        return [el.dataset.tip + (keys ? `  ${keys}` : '')];
    }
    return el._tip?.() ?? null;
}

function showTip(el) {
    const lines = tipFor(el);
    if (!lines?.length || openPop || el.classList.contains('dragging')) return;
    tip.replaceChildren(...lines.filter(Boolean).map((line, i) => {
        const div = document.createElement('div');
        div.className = i === 0 ? 'tip-title' : 'tip-line';
        div.textContent = line;
        return div;
    }));
    tip.hidden = false;
    const r = el.getBoundingClientRect();
    const gap = 8;
    const width = tip.offsetWidth, height = tip.offsetHeight;
    const below = r.bottom + gap + height < window.innerHeight - 8;
    tip.style.left = Math.min(Math.max(8, r.left + r.width / 2 - width / 2), window.innerWidth - width - 8) + 'px';
    tip.style.top = (below ? r.bottom + gap : r.top - gap - height) + 'px';
}

function hideTip() {
    clearTimeout(tipTimer);
    tipTarget = null;
    tip.hidden = true;
}

document.addEventListener('mouseover', e => {
    const el = e.target.closest('[data-tip], .chip');
    if (el === tipTarget) return;
    hideTip();
    if (!el) return;
    tipTarget = el;
    tipTimer = setTimeout(() => showTip(el), 450);
});
document.addEventListener('mousedown', hideTip, true);
document.addEventListener('wheel', hideTip, true);

// ---------- New menu ----------

const newButton = document.getElementById('new');
const newMenu = document.getElementById('new-menu');

function newSession() {
    Launcher.NewSession();
    input.focus();
}

async function newFolder() {
    try {
        await Launcher.AddFolder();
    } catch (err) {
        if (view) addText(view, 'system error', String(err));
    }
    input.focus();
}

newButton.addEventListener('click', () => togglePop(newMenu, newButton, 'left'));
newMenu.addEventListener('click', e => {
    const item = e.target.closest('.menu-item');
    if (!item) return;
    closePop();
    if (item.dataset.action === 'folder') newFolder();
    else newSession();
});

// ---------- context menu ----------

const contextMenu = document.getElementById('context-menu');

// items: {label, detail, danger, disabled, confirm, run}. An item with
// confirm asks again in place before it runs.
function showContextMenu(e, items) {
    e.preventDefault();
    const list = contextMenu.querySelector('.popover-scroll');
    list.replaceChildren(...items.map(it => {
        const b = document.createElement('button');
        b.type = 'button';
        b.className = 'menu-item' + (it.danger ? ' danger' : '');
        b.setAttribute('role', 'menuitem');
        b.disabled = !!it.disabled;
        const label = document.createElement('span');
        label.className = 'menu-label';
        label.textContent = it.label;
        b.appendChild(label);
        if (it.detail) {
            const detail = document.createElement('span');
            detail.className = 'menu-detail';
            detail.textContent = it.detail;
            b.appendChild(detail);
        }
        b.addEventListener('click', () => {
            if (it.confirm && !b.classList.contains('confirming')) {
                b.classList.add('confirming');
                label.textContent = it.confirm;
                return;
            }
            closePop();
            it.run();
        });
        return b;
    }));
    const point = new DOMRect(e.clientX, e.clientY, 0, 0);
    showPop(contextMenu, point, 'left');
}

// ---------- settings: agent, model, effort ----------

// The gear in the tray opens a popover with one expandable section each for
// the agent, model and effort; a section's header shows the current choice.
// Choosing an option keeps the popover open.
const settingsPop = document.getElementById('settings-pop');
const settingsButton = document.getElementById('settings-button');
const sections = Object.fromEntries([...settingsPop.querySelectorAll('.expander')].map(el => [el.dataset.kind, el]));
const effortNames = {minimal: 'Minimal', low: 'Low', medium: 'Medium', high: 'High', xhigh: 'Extra high', max: 'Max', ultra: 'Ultra'};

let agents = [];
let selection = {agent: 'claude', model: '', effort: ''};

for (const section of Object.values(sections)) {
    const head = section.querySelector('.expander-head');
    head.addEventListener('click', () => {
        // One section is open at a time.
        const open = head.getAttribute('aria-expanded') !== 'true';
        for (const other of Object.values(sections)) {
            const on = other === section && open;
            other.querySelector('.expander-head').setAttribute('aria-expanded', String(on));
            other.classList.toggle('open', on);
        }
    });
}

function currentAgent() {
    return agents.find(a => a.id === selection.agent);
}

function currentModel() {
    const models = currentAgent()?.models ?? [];
    return models.find(m => m.value === selection.model) ?? models.find(m => m.value === 'default') ?? models[0];
}

function modelName(m) {
    return m?.label.replace(/\s*\(.*\)$/, '') ?? '';
}

// options returns the rows for one section: {label, description, active,
// disabled, pick}.
function options(kind) {
    if (kind === 'agent') {
        // The custom agent is set up in the config file only.
        return agents.filter(a => a.id !== 'custom').map(a => ({
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
            // Only Default explains itself; the rest are just names.
            description: m.value === 'default' ? m.description : '',
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

// renderSettings updates the gear's tooltip and, while open (or when force
// is set), the popover.
function renderSettings(force = false) {
    const values = {
        agent: currentAgent()?.name ?? 'Claude',
        model: modelName(currentModel()),
        effort: selection.effort ? (effortNames[selection.effort] ?? selection.effort) : 'Auto',
    };
    settingsButton.dataset.tip = ['Settings', values.agent, values.model,
        currentModel()?.efforts?.length ? values.effort + ' effort' : ''].filter(Boolean).join(' · ');
    if (!force && openPop?.el !== settingsPop) return;

    for (const [kind, section] of Object.entries(sections)) {
        const rows = options(kind);
        section.hidden = kind !== 'agent' && rows.length === 0;
        section.querySelector('.expander-value').textContent = values[kind];
        section.querySelector('.option-list').replaceChildren(...rows.map(optionButton));
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
    repositionPop();
}

settingsButton.addEventListener('click', () => togglePop(settingsPop, settingsButton, 'right', () => {
    // Render before showing so the popover is measured at its real size.
    renderSettings(true);
    loadAgents();
}));
Events.On('agents:changed', loadAgents);

// Clicks on the empty stage around the cards act like clicking outside the
// window: close the open popover, or hide.
stage.addEventListener('mousedown', e => {
    if (openPop && !openPop.el.contains(e.target) && !openPop.button?.contains(e.target)) {
        closePop();
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
    // Claude Code may have changed sessions while the window was hidden.
    loadSessions();
    loadFolders();
});

// ---------- folders ----------

// The tray's first row holds All and the folders, between the New and
// settings buttons; the second row holds the sessions of the selected one.
// Folders can be dragged to reorder them (pinned ones stay first).
const folderRow = document.getElementById('folder-row');
const sessionRow = document.getElementById('session-row');
const statusNames = {
    idle: 'Idle',
    running: 'Running',
    waiting: 'Finished, waiting for you',
    error: 'Stopped with an error',
};
const folderStatusNames = {
    idle: 'No sessions running',
    running: 'A session is running',
    waiting: 'A reply is waiting for you',
    error: 'A session stopped with an error',
};
const maxFolderName = 24;
const maxSessionName = 36;
const maxTitle = 100; // matches the backend's limit

let folders = {selected: '', cwd: '', home: '', allStatus: 'idle', folders: []};

function clip(text, max) {
    return text.length > max ? text.slice(0, max - 1).trimEnd() + '…' : text;
}

// tildePath shortens a path in the home folder to ~/….
function tildePath(path) {
    const home = folders.home;
    if (!home || !path) return path ?? '';
    if (path === home) return '~';
    return path.startsWith(home + '/') || path.startsWith(home + '\\') ? '~' + path.slice(home.length) : path;
}

// chip builds a pill for the folder or session rows.
function chip(className, label, status, max, tipLines) {
    const b = document.createElement('button');
    b.type = 'button';
    b.className = 'chip ' + className;
    b.dataset.status = status;
    const dot = document.createElement('span');
    dot.className = 'status';
    const name = document.createElement('span');
    name.className = 'chip-name';
    name.textContent = clip(label, max);
    b.append(dot, name);
    b._tip = tipLines;
    return b;
}

async function loadFolders() {
    folders = await Launcher.GetFolders();
    renderFolders();
    updatePlaceholder();
}

function renderFolders() {
    const all = chip('folder all' + (folders.selected === '' ? ' active' : ''), 'All', folders.allStatus, maxFolderName,
        () => ['All folders', `New sessions start in ${tildePath(folders.cwd)}`, folderStatusNames[folders.allStatus]]);
    all.setAttribute('role', 'tab');
    all.addEventListener('click', () => selectFolder(''));

    folderRow.replaceChildren(all, ...folders.folders.map(f => {
        const b = chip('folder' + (f.path === folders.selected ? ' active' : '') + (f.exists ? '' : ' missing'),
            f.name, f.status, maxFolderName,
            () => [f.name, tildePath(f.path), f.exists ? folderStatusNames[f.status] : 'Folder not found', f.pinned ? 'Pinned' : '']);
        b.setAttribute('role', 'tab');
        b.dataset.path = f.path;
        b.dataset.pinned = f.pinned;
        if (f.pinned) b.prepend(pinIcon());
        b.addEventListener('click', () => {
            if (!b.dataset.dragged) selectFolder(f.path);
        });
        b.addEventListener('contextmenu', e => showContextMenu(e, [
            {label: isMac ? 'Open in Finder' : 'Open in Explorer', disabled: !f.exists, run: () => Launcher.OpenFolder(f.path)},
            {label: f.pinned ? 'Unpin folder' : 'Pin folder', run: () => Launcher.PinFolder(f.path, !f.pinned)},
            {label: 'Delete folder', detail: 'Removes it from cmdcmd; files stay on disk', danger: true,
                confirm: 'Click again to delete',
                run: () => Launcher.RemoveFolder(f.path)},
        ]));
        b.addEventListener('pointerdown', e => startFolderDrag(e, b));
        return b;
    }));
    folderRow.querySelector('.active')?.scrollIntoView({inline: 'nearest', block: 'nearest'});
}

function pinIcon() {
    const svg = document.createElementNS('http://www.w3.org/2000/svg', 'svg');
    svg.setAttribute('viewBox', '0 0 24 24');
    svg.classList.add('pin');
    svg.innerHTML = '<path d="M9 4h6l-1 6 3 3v2H7v-2l3-3-1-6zM12 15v5"/>';
    return svg;
}

function selectFolder(path) {
    if (path === folders.selected) return;
    folders.selected = path;
    for (const el of folderRow.children) el.classList.toggle('active', (el.dataset.path ?? '') === path);
    Launcher.SelectFolder(path);
    input.focus();
}

// startFolderDrag lets a folder be dragged sideways to a new place among the
// folders with the same pinned state. A press that barely moves is a click.
function startFolderDrag(e, el) {
    if (e.button !== 0) return;
    const startX = e.clientX;
    const grab = e.clientX - el.getBoundingClientRect().left;
    let dragging = false;
    delete el.dataset.dragged;

    const peers = () => [...folderRow.querySelectorAll('.folder:not(.all)')]
        .filter(p => p !== el && p.dataset.pinned === el.dataset.pinned);

    const move = ev => {
        if (!dragging) {
            if (Math.abs(ev.clientX - startX) < 5) return;
            dragging = true;
            el.dataset.dragged = '1';
            el.classList.add('dragging');
            el.setPointerCapture(ev.pointerId);
            hideTip();
        }
        // Move the folder before the first peer whose middle is right of
        // the pointer, or after the last one.
        const group = peers();
        const before = group.find(p => {
            const r = p.getBoundingClientRect();
            return ev.clientX < r.left + r.width / 2;
        });
        if (before && before !== el.nextElementSibling) before.before(el);
        else if (!before && group.length && group[group.length - 1].nextElementSibling !== el) group[group.length - 1].after(el);

        el.style.transform = '';
        const left = el.getBoundingClientRect().left;
        el.style.transform = `translateX(${ev.clientX - grab - left}px)`;
    };
    const up = () => {
        window.removeEventListener('pointermove', move);
        window.removeEventListener('pointerup', up);
        window.removeEventListener('pointercancel', up);
        if (!dragging) return;
        el.classList.remove('dragging');
        el.style.transform = '';
        // Let the click that ends the drag pass before allowing clicks again.
        setTimeout(() => delete el.dataset.dragged, 0);
        const order = [...folderRow.querySelectorAll('.folder:not(.all)')].map(f => f.dataset.path);
        Launcher.ReorderFolders(order);
    };
    window.addEventListener('pointermove', move);
    window.addEventListener('pointerup', up);
    window.addEventListener('pointercancel', up);
}

// The placeholder shows the path the session on screen runs in.
function updatePlaceholder() {
    const cwd = sessions.currentCwd || folders.selected || folders.cwd;
    input.placeholder = cwd ? `Ask AI in ${tildePath(cwd)}` : 'Ask AI';
}

Events.On('folders:changed', loadFolders);

// ---------- sessions ----------

// The symbol before each session's name shows its status; Tab and
// Shift+Tab step through them.
let sessions = {current: '', currentCwd: '', sessions: []};
let activeIndex = -1; // the session on screen, or the one Tab last stepped to

async function loadSessions() {
    sessions = await Launcher.GetSessions();
    dropViews(new Set(sessions.sessions.map(s => s.key).filter(Boolean)));
    activeIndex = sessions.sessions.findIndex(s => s.key === sessions.current);
    renderSessions();
    updatePlaceholder();
    showOutsideTurn();
}

// A turn run from Claude Code on the session on screen shows as thinking
// too, timed from when cmdcmd noticed it.
function showOutsideTurn() {
    const v = views.get(sessions.current);
    if (!v || v.pending > 0) return;
    const running = sessions.sessions.find(s => s.key === sessions.current)?.status === 'running';
    if (running && !v.outside) {
        v.outside = true;
        v.turnStart = Date.now();
        setThinking(v, true);
    } else if (!running && v.outside) {
        v.outside = false;
        v.turnStart = null;
        setThinking(v, false);
    }
}

function renderSessions() {
    if (sessionRow.querySelector('.chip-rename')) return; // don't pull the field away mid-rename
    sessionRow.replaceChildren(...sessions.sessions.map((s, i) => {
        const title = s.title || 'New session';
        const b = chip('session' + (i === activeIndex ? ' active' : ''), title, s.status, maxSessionName,
            () => [title, tildePath(s.cwd), `${statusNames[s.status] ?? ''} · ${timeAgo(s.updated)}`, s.pinned ? 'Pinned' : '']);
        b.setAttribute('role', 'option');
        if (s.pinned) b.prepend(pinIcon());
        b.addEventListener('click', () => openSession(i));
        b.addEventListener('contextmenu', e => showContextMenu(e, [
            {label: 'Rename…', run: () => renameSession(b, s, title)},
            {label: 'Copy resume command', disabled: !s.id, detail: s.id ? '' : 'Send a message first',
                run: () => Launcher.CopyResumeCommand(s).catch(err => view && addText(view, 'system error', String(err)))},
            {label: s.pinned ? 'Unpin session' : 'Pin session', disabled: !s.id,
                detail: s.id ? '' : 'Send a message first', run: () => Launcher.PinSession(s, !s.pinned)},
            {label: 'Delete session', confirm: 'Click again to delete', danger: true,
                run: () => Launcher.DeleteSession(s).catch(err => view && addText(view, 'system error', String(err)))},
        ]));
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

// renameSession swaps the chip for a text field; Enter or leaving it saves,
// Esc cancels.
function renameSession(b, s, title) {
    const field = document.createElement('input');
    field.type = 'text';
    field.className = 'chip-rename';
    field.value = title;
    field.maxLength = maxTitle;
    field.spellcheck = false;
    b.replaceWith(field);
    field.focus();
    field.select();
    let done = false;
    const finish = save => {
        if (done) return;
        done = true;
        const name = field.value.trim();
        field.replaceWith(b);
        input.focus();
        if (save && name && name !== title) {
            b.querySelector('.chip-name').textContent = clip(name, maxSessionName);
            Launcher.RenameSession(s, name).catch(err => view && addText(view, 'system error', String(err)));
        }
    };
    field.addEventListener('keydown', e => {
        e.stopPropagation(); // keep Esc, Tab and typing out of the window's shortcuts
        if (e.key === 'Enter') finish(true);
        else if (e.key === 'Escape') finish(false);
    });
    field.addEventListener('blur', () => finish(true));
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

// A vertical wheel scrolls the rows sideways.
for (const row of [folderRow, sessionRow]) {
    row.addEventListener('wheel', e => {
        if (Math.abs(e.deltaY) <= Math.abs(e.deltaX)) return;
        e.preventDefault();
        row.scrollLeft += e.deltaY;
    }, {passive: false});
}

// Session statuses roll up into the folders, so both reload together.
Events.On('sessions:changed', () => {
    loadSessions();
    loadFolders();
});

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

// Another client (Claude Code in a terminal) added messages to a session.
Events.On('session:append', ({data}) => {
    const v = views.get(data.key);
    if (!v) return;
    v.current = null;
    for (const it of data.items) {
        if (it.role === 'user') addText(v, 'user', it.text);
        else if (it.role === 'assistant') addMarkdown(v, it.text);
        else addTool(v, it.tool, it.text);
    }
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
loadFolders();
loadSessions().then(() => showView(sessions.current));

updateLayout();
