// T.I.T.A.N in the browser, for the demo page: a fake homelab that plays a
// one-minute story on a loop and answers every /api route the page uses.
// Nothing leaves the browser. demo/static/build.py puts this in front of
// the real page (internal/web/page.html).
(() => {
  const LOOP = 60;
  const now = () => Date.now() / 1000;
  const loopStart = t => Math.floor(t / LOOP) * LOOP;
  const into = t => t - loopStart(t);

  // ---- people ----
  const users = { wizard: 'owner', sam: 'family', alex: 'friend' };
  let who = null;
  try { who = sessionStorage.getItem('titan-demo-who'); } catch {}
  if (who && !users[who]) who = null;
  const role = () => users[who];
  const rank = { friend: 1, family: 2, owner: 3 };
  const atLeast = (r, need) => rank[r] >= rank[need];

  // ---- the services ----
  const S = [
    { id: 'enshrouded', name: 'Enshrouded', group: 'Games', audience: 'friend', actions: ['start_service', 'restart_service'], join: "Server: Wizard's World at 192.168.20.5:15637. The password is pinned in Discord #games." },
    { id: 'valheim', name: 'Valheim', group: 'Games', audience: 'friend', actions: ['restart_service'] },
    { id: 'plex', name: 'Plex', group: 'Media', audience: 'friend', actions: ['restart_service'], fixBy: 'family' },
    { id: 'media', name: 'Media pipeline', group: 'Media', audience: 'family', actions: [] },
    { id: 'faith', name: 'FAITH', group: 'Family', audience: 'family', actions: ['restart_service', 'recent_problems'] },
    { id: 'dnd', name: 'DnD sync', group: 'Family', audience: 'family', actions: ['run_sync'] },
    { id: 'immich', name: 'Immich (photos)', group: 'Family', audience: 'family', actions: ['restart_service'] },
    { id: 'mealie', name: 'Mealie (recipes)', group: 'Family', audience: 'family', actions: ['restart_service'] },
    { id: 'allglance', name: 'ALLGlance (calendar)', group: 'Family', audience: 'family', actions: [] },
    { id: 'tunnel', name: 'Outside access', group: 'Homelab', audience: 'owner', actions: ['restart_service'] },
    { id: 'argos', name: 'The AI (Argos)', group: 'Homelab', audience: 'family', actions: [], fixBy: 'owner' },
    ...['binhex-readarr', 'binhex-sonarr', 'binhex-radarr', 'sabnzbd', 'tautulli', 'overseer', 'hermes-agent', 'hermes-studio', 'odysseus-ntfy-1', 'croniclev2', 'homeassistant', 'uptime-kuma'].map(n => ({ id: n, name: n, group: 'Containers', audience: 'owner', actions: [], found: true })),
  ];
  const byId = Object.fromEntries(S.map(s => [s.id, s]));
  const sees = s => role() && atLeast(role(), s.audience);
  const mayFix = s => atLeast(role(), s.fixBy || s.audience);

  // What the people in the demo did this loop (cleared when the loop restarts).
  let did = { loop: 0, started: {}, synced: false, quiet: {}, working: {} };
  const fresh = () => { const l = loopStart(now()); if (did.loop !== l) did = { loop: l, started: {}, synced: false, quiet: did.quiet, working: {} }; };

  // ---- the one-minute story ----
  // state(id, t) says what a check at time t would find.
  function state(id, t) {
    const x = into(t), sameLoop = loopStart(t) === did.loop;
    switch (id) {
      case 'enshrouded':
        if (sameLoop && did.started.enshrouded && t >= did.started.enshrouded) return t - did.started.enshrouded < 6 ? [false, 'Starting (loading the world)'] : [true, 'Ready, ' + (x > 40 ? 2 : 0) + ' of 16 players on'];
        return [false, "Off (the AMP instance isn't running)"];
      case 'faith':
        if (sameLoop && did.started.faith && t >= did.started.faith && x < 28) return [true, 'Answering'];
        return x >= 12 && x < 28 ? [false, 'Answering with an error (502)'] : [true, 'Answering'];
      case 'dnd':
        if (sameLoop && did.synced && t >= did.synced) return [true, 'Last ran ' + Math.max(1, Math.round((t - did.synced))) + ' s ago'];
        return [false, 'Last ran 30 h ago (should be under 6 h)'];
      case 'media':
        return x < 32 ? [false, "Stuck: Andor S2E1: downloaded but can't be moved into the library: No files found are eligible for import"] : [true, 'Moving: 2 downloading'];
      case 'binhex-readarr':
        return x >= 36 && x < 56 ? [false, 'Running but unhealthy'] : [true, 'Running, healthy'];
      case 'plex': return [true, 'Answering'];
      case 'argos': return [true, 'Answering'];
      case 'tunnel': return [true, 'Running for 26 h'];
      default: return [true, byId[id].found ? 'Running' : 'Running'];
    }
  }
  const info = (id, t) => {
    const x = into(t);
    if (id === 'plex') return x >= 30 && x < 50 ? ['4 people watching (3 converting, which is harder on the server)'] : ['2 people watching (1 converting)'];
    if (id === 'argos') return ['Loaded: qwen3-8b'];
    return [];
  };

  // Things that happen at a second of the loop: [second, kind, ...]
  const story = [
    [5, 'room', 'sam', 'anyone on enshrouded tonight?'],
    [12, 'down', 'faith', 'Went down: Answering with an error (502)'],
    [12, 'note', 'down', 'FAITH is down: Answering with an error (502)'],
    [15, 'room', 'alex', 'plex is a bit laggy lol'],
    [20, 'titan', 'faith', "T.I.T.A.N looked: the logs say it ran out of memory. It tried to restart FAITH by itself: faith restarted. I'll check it again in a moment."],
    [28, 'titan', 'faith', 'Back up: Answering'],
    [32, 'titan', 'media', 'Back up: Moving: 2 downloading'],
    [36, 'titan', 'binhex-readarr', 'Went down: Running but unhealthy'],
    [44, 'titan', 'binhex-readarr', "T.I.T.A.N looked: needs Wizard. Its health check fails with 'database is locked', which a restart won't fix."],
    [44, 'note', 'needs_owner', "binhex-readarr: its health check fails with 'database is locked', which a restart won't fix."],
    [46, 'room', 'sam', '/titan is plex ok?'],
    [47, 'roombot', 'sam', '@sam ✅ Plex up. 4 watching, 3 converting. Pick lower quality if it lags.'],
    [56, 'titan', 'binhex-readarr', 'Back up: Running, healthy'],
  ];

  // ---- what has been said and done ----
  let nextId = 1;
  const updates = []; // {id, service, at, author, bot, text}
  const room = [];    // {id, at, author, bot, text, removed, proposals}
  const notes = [];   // {id, at, kind, name, text}
  let played = now() - 3 * LOOP; // start with three minutes of history
  function play() {
    fresh();
    const t1 = now();
    for (let l = loopStart(played); l <= t1; l += LOOP) {
      for (const [sec, kind, a, b] of story) {
        const at = l + sec;
        if (at <= played || at > t1) continue;
        if (quietFor(a, at)) continue;
        if (kind === 'room') room.push({ id: nextId++, at, author: a, bot: false, text: b });
        else if (kind === 'roombot') room.push({ id: nextId++, at, author: 'titan', bot: true, text: b });
        else if (kind === 'note') notes.unshift({ id: nextId++, at, kind: a, name: 'titan', text: b });
        else updates.push({ id: nextId++, service: a, at, author: 'titan', bot: true, text: b });
      }
    }
    played = t1;
    while (updates.length > 400) updates.shift();
    while (room.length > 200) room.shift();
    while (notes.length > 30) notes.pop();
  }
  function setQuiet(id, hours, why) {
    did.quiet[id] = { until: hours > 0 ? now() + hours * 3600 : 0, by: who, why: why || '', since: now() };
    const ids = id === '*' ? Object.keys(byId).filter(k => !byId[k].found) : [id];
    for (const k of ids) {
      const w = did.working[k] = (did.working[k] || []).filter(x => x !== who); w.push(who);
    }
    updates.push({ id: nextId++, service: id === '*' ? 'plex' : id, at: now(), author: 'titan', bot: true, text: '🤫 ' + who + ' is working on it' + (why ? ': ' + why : '') + ". Alerts and automatic fixes are off until it's confirmed up (or up for 2 hours)." });
  }
  function liftQuiet(id, line) {
    const q = did.quiet[id]; if (!q) return;
    delete did.quiet[id];
    const ids = id === '*' ? Object.keys(byId) : [id];
    for (const k of ids) did.working[k] = (did.working[k] || []).filter(x => x !== q.by);
    if (id !== '*') updates.push({ id: nextId++, service: id, at: now(), author: 'titan', bot: true, text: line });
  }
  const quietFor = (id, t) => { const q = did.quiet[id] || did.quiet['*']; return q && (!q.until || q.until > t) && byId[id]; };

  // ---- the board ----
  function card(s) {
    const t = now(), [ok, detail] = state(s.id, t);
    const beats = [], beatAt = [];
    for (let k = 39; k >= 0; k--) { const at = Math.floor(t - k * 3); beats.push(state(s.id, at)[0] ? 1 : 0); beatAt.push(at); }
    const up = 100 * beats.filter(Boolean).length / beats.length;
    const marks = updates.filter(u => u.service === s.id && u.at >= beatAt[0]).map(u => ({ at: u.at, who: u.bot ? 'T.I.T.A.N' : u.author, text: u.text }));
    const actions = [{ action: 'check_now', label: 'Check again' }];
    for (const a of s.actions) {
      if (a === 'recent_problems' && role() !== 'owner') continue;
      if (a !== 'recent_problems' && !mayFix(s)) continue;
      actions.push({ action: a, label: { start_service: 'Start', restart_service: 'Restart', run_sync: 'Run sync', recent_problems: 'Recent problems' }[a] });
    }
    let quiet = did.quiet[s.id] ? { service: s.id, ...did.quiet[s.id] } : did.quiet['*'] ? { service: '*', ...did.quiet['*'] } : null;
    if (quiet && role() !== 'owner') quiet = { service: quiet.service, until: quiet.until };
    return {
      id: s.id, name: s.name, group: s.group, status: ok ? 'up' : 'down', detail, checked_at: Math.floor(t) - (Math.floor(t) % 3), ms: 40,
      uptime: { '24h': Math.round(up * 10) / 10, '7d': Math.min(100, Math.round((up + 2) * 10) / 10), '30d': 99.2 },
      beat_at: beatAt, beats, marks, info: info(s.id, t), updates_24h: updates.filter(u => u.service === s.id && u.at > t - 86400).length,
      working: did.working[s.id] || [], actions, found: !!s.found, quiet: quiet || undefined,
    };
  }

  // ---- the helper (scripted, caveman) ----
  const proposals = {};
  let lastLong = '';
  function propose(s, action) {
    const id = Math.random().toString(16).slice(2, 14);
    proposals[id] = { service: s.id, action, by: who };
    const label = { start_service: 'Start ', restart_service: 'Restart ', run_sync: 'Run ', check_now: 'Check ' }[action] + (action === 'run_sync' ? s.name + ' now' : action === 'check_now' ? s.name + ' again' : s.name);
    return { id, service: s.id, name: s.name, action, label };
  }
  const visible = () => S.filter(sees);
  const named = text => visible().filter(s => text.includes(s.id) || text.includes(s.name.toLowerCase().split(' (')[0]));
  function helper(text, asRoom) {
    const r0 = role(), t = text.toLowerCase(), cap = asRoom && r0 !== 'friend' ? 'friend' : r0;
    const say = (reply, long, extra = {}) => { lastLong = long; return { reply, verdict: { verdict: 'allow', reason: 'A status question or a fix.', model: 'respan/span-01-lite' }, model: 'titan-agent', calls: [], ...extra }; };
    if (/\b(rm -rf|delete|wipe|format)\b/.test(t)) return { reply: "I can't help with that. That would delete or break things. I've let Wizard know.", verdict: { verdict: 'refuse' } };
    if (/password|api key|token/.test(t) && !/join|enshrouded|game/.test(t)) return { reply: "I can't help with that. It asks for a password or other secret, which only the owner hands out. I've let Wizard know.", verdict: { verdict: 'refuse' } };
    if (/ignore (your|all|previous)|system prompt|developer mode/.test(t)) return { reply: "I can't help with that. It tries to get round T.I.T.A.N's rules. I've let Wizard know.", verdict: { verdict: 'refuse' } };
    if (/install|admin account|make me (an )?admin/.test(t)) return { reply: "That's one for Wizard, so I've passed it on. (Setting things up or changing accounts is the owner's call.)", verdict: { verdict: 'ask_owner' } };
    if (/^(thanks|thank you|ty|ok|nice|cool)\b/.test(t)) return say('👍', '');
    if (r0 === 'owner' && !asRoom && /(messing|working on|playing with|don.?t worry)/.test(t)) {
      const s = named(t)[0];
      const id = s ? s.id : '*';
      setQuiet(id, 0, text.slice(0, 80));
      return say('🤫 ' + (s ? s.name : 'Everything') + ' quiet. No alerts.', 'I made ' + (s ? s.name : 'everything') + " quiet and said you're working on it: no alerts and no automatic fixes until someone presses Confirmed up, or it has been up for 2 hours.");
    }
    const ns = named(t);
    if (/faith/.test(t) && !atLeast(cap, 'family')) {
      notes.unshift({ id: nextId++, at: now(), kind: 'request', name: who, text: who + ' asked about FAITH: "' + text + '"' });
      return say('🤷 FAITH not on your board. Wizard told.', "FAITH is the family assistant, so it isn't on your board. I've passed your message to Wizard.");
    }
    if (/enshrouded|game|join|let us in|server/.test(t) && sees(byId.enshrouded)) {
      const [ok, d] = state('enshrouded', now()), s = byId.enshrouded;
      if (!ok && d.startsWith('Off')) return say('🔴 Enshrouded off. Me start it?', 'Enshrouded runs inside AMP and its instance is off, which is why nobody can get in. Press Yes and T.I.T.A.N starts just that instance; worlds take a minute to load.\n🎮 ' + s.join, { proposals: [propose(s, 'start_service')] });
      return say('✅ Enshrouded up. ' + d + '.\n🎮 Enshrouded: ' + s.join, 'Enshrouded is running. If you still cannot join, the problem is usually the game version or the password, not the server.');
    }
    if (/plex|buffer|lag/.test(t) && sees(byId.plex)) {
      const i = info('plex', now())[0];
      return say('🔧 Plex busy: ' + i.split(' (')[0] + '. Pick lower quality.', 'Plex is up, but ' + i + '. Converting video is heavy work for the server, so picking a lower quality (or Original if your device supports it) usually stops the buffering.');
    }
    if (/faith|slow/.test(t) && sees(byId.faith)) {
      const [ok, d] = state('faith', now());
      if (!ok) return say('🔴 FAITH down. Me restart it?', 'FAITH is answering with an error. A restart usually fixes it; T.I.T.A.N may already be on it by itself.', { proposals: mayFix(byId.faith) ? [propose(byId.faith, 'restart_service')] : [] });
      return say('✅ FAITH up. First question can take a minute.', 'FAITH is answering. It thinks with Argos, which loads a model on the first question, so that one can take up to a minute.');
    }
    if (/dnd|wiki|notes|legendkeeper/.test(t) && sees(byId.dnd)) {
      const [ok] = state('dnd', now());
      if (!ok) return say('🔧 DnD sync old. Run now?', 'The DnD sync last ran 30 hours ago. Press Yes to run it now.', { proposals: [propose(byId.dnd, 'run_sync')] });
      return say('✅ DnD sync fresh.', 'The DnD sync ran recently.');
    }
    if (/severance|andor|show|movie|episode|download|coming/.test(t) && sees(byId.media)) {
      const x = into(now()), p = Math.min(99, Math.round(40 + x));
      return say('📦 Severance S2E4 ' + p + '% done. About ' + Math.max(1, Math.round((100 - p) / 5)) + ' min left.', 'Severance S2E4 is downloading in SABnzbd and will show up on Plex a minute or two after it finishes.');
    }
    if (/down|broken|what.?s wrong|status|up\??$/.test(t) || ns.length === 0) {
      const down = visible().filter(s => !state(s.id, now())[0]).map(s => s.name);
      return say(down.length ? '🔴 ' + down.join(', ') + '.' : '✅ All fine.', down.length ? 'These are down right now: ' + down.join(', ') + '. Press Ask T.I.T.A.N on a card to look at one.' : 'Everything on your board is answering.');
    }
    const s = ns[0], [ok, d] = state(s.id, now());
    return say((ok ? '✅ ' : '🔴 ') + s.name + ': ' + d + '.', s.name + ' says: ' + d + '.');
  }

  // ---- fixes ----
  function run(p) {
    const s = byId[p.service], t = now();
    if (p.action === 'check_now') { const [ok, d] = state(s.id, t); return s.name + (ok ? ' is up: ' : ' is down: ') + d; }
    if (p.action === 'run_sync') { did.synced = t; updates.push({ id: nextId++, service: s.id, at: t, author: 'titan', bot: true, text: p.by + ' asked T.I.T.A.N to run ' + s.name + ' now: the dnd sync has started.' }); return 'The dnd sync has started.'; }
    did.started[s.id] = t;
    const verb = p.action === 'start_service' ? 'start' : 'restart';
    updates.push({ id: nextId++, service: s.id, at: t, author: 'titan', bot: true, text: p.by + ' asked T.I.T.A.N to ' + verb + ' ' + s.name + ': done.' });
    return s.id === 'enshrouded' ? "Enshrouded01 is starting. Games can take a few minutes. I'll check it again in a moment." : s.id + ' ' + verb + "ed. I'll check it again in a moment.";
  }

  // ---- the room's word filter ----
  const bad = ['fuck', 'shit', 'bitch', 'cunt', 'dick', 'asshole', 'bastard'];
  const mask = text => text.replace(/[a-z0-9@$!]+/gi, w => {
    const n = w.toLowerCase().replace(/1/g, 'i').replace(/0/g, 'o').replace(/[@4]/g, 'a').replace(/3/g, 'e').replace(/\$/g, 's');
    return bad.some(b => n.startsWith(b)) ? w[0] + '*'.repeat(w.length - 1) : w;
  });

  // ---- the routes ----
  const ok = (body, status = 200) => new Response(JSON.stringify(body), { status, headers: { 'Content-Type': 'application/json' } });
  const no = (status, error) => ok({ error }, status);
  const logs = {
    faith: ['[server] FAITH listening on :8097', '[harness] model titan-agent answered in 2.1 s', '[server] ERROR JavaScript heap out of memory', '[auth] token=[removed] refreshed', '[server] Restarting after a crash'],
    plex: ['Transcoder: 3 sessions', 'Library scan finished'],
  };
  async function route(method, path, body) {
    play();
    const url = new URL(path, location.href), p = url.pathname;
    if (p === '/api/login' && method === 'POST') {
      const name = String(body.name || '').trim().toLowerCase();
      if (!users[name]) return no(401, 'Try wizard (owner), sam (family) or alex (friend). Any password works in the demo.');
      who = name; try { sessionStorage.setItem('titan-demo-who', who); } catch {}
      return ok({ name, role: users[name] });
    }
    if (!who) return no(401, 'Sign in first.');
    const r = role();
    if (p.startsWith('/api/owner/') && r !== 'owner') return no(403, 'That page is the owner\'s.');
    if (p === '/api/logout') { who = null; try { sessionStorage.removeItem('titan-demo-who'); } catch {} return ok({ ok: true }); }
    if (p === '/api/me') return ok({ name: who, role: r, owner_name: 'Wizard', faith_url: '', server_view: r === 'owner' });
    if (p === '/api/board') return ok({ services: visible().map(card) });
    if (p === '/api/chat') return ok(helper(String(body.message || ''), false));
    if (p === '/api/explain') return ok({ reply: lastLong || 'Nothing to explain yet.' });
    let m;
    if ((m = p.match(/^\/api\/updates\/(.+)$/))) {
      const s = byId[decodeURIComponent(m[1])];
      if (!s || !sees(s)) return no(404, "There's no service like that on your board.");
      if (method === 'POST') {
        if (body.working !== undefined) { const w = did.working[s.id] = (did.working[s.id] || []).filter(x => x !== who); if (body.working) w.push(who); }
        const text = (body.text || '').trim() || (body.working === true ? 'is working on it' : body.working === false ? 'stopped working on it' : '');
        if (!text) return no(400, 'Write something first.');
        updates.push({ id: nextId++, service: s.id, at: now(), author: who, bot: false, text });
        return ok({ ok: true });
      }
      const [okk, detail] = state(s.id, now());
      return ok({ service: s.id, name: s.name, updates: updates.filter(u => u.service === s.id).slice().reverse().slice(0, 100), working: did.working[s.id] || [], checked: [{ at: now() - 2, ok: okk, detail }] });
    }
    if (p === '/api/propose') {
      const s = byId[body.service];
      if (!s || !sees(s)) return no(404, "There's no service like that on your board.");
      if (body.action !== 'check_now' && (!s.actions.includes(body.action) || !mayFix(s))) return no(403, body.action + " isn't allowed on " + s.name + ' for a ' + r + '.');
      return ok(propose(s, body.action));
    }
    if ((m = p.match(/^\/api\/proposals\/(.+)$/))) {
      const pr = proposals[decodeURIComponent(m[1])];
      if (!pr) return no(410, "That fix isn't waiting any more. Ask again if it's still needed.");
      delete proposals[decodeURIComponent(m[1])];
      if (!body.yes) return ok({ result: 'OK, left alone.' });
      return ok({ result: run(pr) });
    }
    if (p === '/api/room' && method === 'GET') {
      const after = +url.searchParams.get('after') || 0;
      return ok({ lines: room.filter(l => l.id > after) });
    }
    if (p === '/api/room' && method === 'POST') {
      const text = String(body.text || '').trim();
      if (!text) return no(400, 'Write something first.');
      room.push({ id: nextId++, at: now(), author: who, bot: false, text: mask(text) });
      const a = text.match(/^\/(agent|titan)\s+(.*)/i);
      if (a) { const turn = helper(a[2], true); room.push({ id: nextId++, at: now(), author: 'titan', bot: true, text: '@' + who + ' ' + turn.reply, proposals: turn.proposals }); }
      return ok({ ok: true });
    }
    if ((m = p.match(/^\/api\/owner\/room\/(\d+)$/))) { const l = room.find(x => x.id === +m[1]); if (l) l.removed = true; return ok({ ok: true }); }
    if (p === '/api/server') {
      if (r !== 'owner') return no(403, "The server view is the owner's.");
      const t = now(), w = (a, b, f) => a + b * (0.5 + 0.5 * Math.sin(t / f));
      const G = 1073741824;
      return ok({ server: { cpu_percent: w(8, 50, 7), cores: 32, load_1m: w(1, 6, 9), mem_used: w(38, 40, 13) * G, mem_total: 128 * G, uptime_seconds: 26 * 86400 + into(t), disks: [{ name: 'array', used: 61.2 * 1024 * G, total: 87.3 * 1024 * G }, { name: 'cache', used: w(700, 500, 20) * G, total: 1863 * G }], gpus: [{ name: 'MI50 #1', busy_percent: w(5, 90, 5), mem_used: w(8, 20, 11) * G, mem_total: 32 * G, temp_c: w(45, 35, 5) }, { name: 'MI50 #2', busy_percent: w(0, 30, 17), mem_used: 9 * G, mem_total: 32 * G, temp_c: w(40, 15, 17) }] } });
    }
    if (p === '/api/activity') {
      const items = [];
      if (r === 'owner') for (const n of notes.slice(0, 8)) items.push({ at: n.at, name: n.name, kind: 'needs_you', text: n.kind.replace('_', ' ') + ': ' + n.text });
      for (const u of updates.slice().reverse()) { const s = byId[u.service]; if (!sees(s)) continue; items.push({ at: u.at, service: s.id, name: s.name, kind: u.bot ? 'titan' : 'person', who: u.bot ? 'T.I.T.A.N' : u.author, text: u.text }); if (items.length > 24) break; }
      items.sort((a, b) => b.at - a.at);
      const out = { items: items.slice(0, 24), now: visible().filter(s => s.group === 'Media' || s.group === 'Games').filter(s => s.id !== 'media').map(s => ({ name: s.name, detail: state(s.id, now())[1], info: info(s.id, now())[0] })) };
      if (atLeast(r, 'family')) {
        const x = into(now()), p2 = Math.min(99, Math.round(40 + x));
        out.media = { verdict: x < 32 ? 'stuck' : 'moving', summary: x < 32 ? 'Stuck: Andor S2E1 can\'t be imported' : 'Moving: 2 downloading', downloader: 'SABnzbd at 42 MB/s', downloads: [{ title: 'Severance S2E4', progress_percent: p2, eta: Math.max(1, Math.round((100 - p2) / 5)) + ' min' }, x < 32 ? { title: 'Andor S2E1', problem: 'downloaded but not imported' } : { title: 'Dune: Part Two', progress_percent: Math.round(x), eta: '9 min' }], recently_on_plex: ['The Bear S3E1', 'Dune: Part Two'] };
      }
      return ok(out);
    }
    if (p === '/api/owner/quiet' && method === 'POST') {
      const id = body.service === 'all' || !body.service ? '*' : body.service;
      setQuiet(id, body.hours, body.why);
      return ok({ result: (id === '*' ? 'Everything' : byId[id].name) + " is quiet and marked as you working on it. It ends when someone presses Confirmed up, or after 2 hours up." });
    }
    if ((m = p.match(/^\/api\/owner\/quiet\/(.+)$/))) { const id = decodeURIComponent(m[1]); liftQuiet(id, '🔔 ' + who + ' lifted quiet. Alerts are back on.'); return ok({ result: (id === '*' ? 'Everything' : byId[id].name) + " isn't quiet any more." }); }
    if ((m = p.match(/^\/api\/confirm\/(.+)$/)) && method === 'POST') {
      const s = byId[decodeURIComponent(m[1])];
      if (!s || !sees(s)) return no(404, "There's no service like that on your board.");
      if (r !== 'owner') return no(403, 'Only Wizard can confirm it\'s up.');
      if (!state(s.id, now())[0]) return no(409, s.name + " still looks down, so it can't be confirmed up yet.");
      liftQuiet(s.id, '✅ ' + who + " confirmed it's up. Alerts are back on.");
      return ok({ result: s.name + ' confirmed up. Alerts are back on.' });
    }
    if ((m = p.match(/^\/api\/owner\/room\/(\d+)$/)) && method === 'DELETE') { const l = room.find(x => x.id === +m[1]); if (l) { l.removed = true; l.text = ''; } return ok({ ok: true }); }
    if (p === '/api/owner/inbox') return ok(notes.map(n => ({ ...n })));
    if ((m = p.match(/^\/api\/owner\/inbox\/(\d+)\/done$/))) { const i = notes.findIndex(n => n.id === +m[1]); if (i >= 0) notes.splice(i, 1); return ok({ ok: true }); }
    if (p === '/api/owner/problems') return ok({ lines: logs[body.service] || ['No log lines in the demo for this one.'] });
    if (p === '/api/owner/users' && method === 'GET') return ok(Object.entries(users).map(([name, role]) => ({ name, role })));
    if (p === '/api/owner/users' && method === 'POST') {
      const name = String(body.name || '').trim().toLowerCase();
      if (!/^[a-z0-9_-]{2,32}$/.test(name)) return no(400, 'Names are 2 to 32 letters, digits, - or _.');
      if (users[name]) return no(400, name + ' already has an account.');
      users[name] = body.role || 'friend'; return ok({ name });
    }
    if ((m = p.match(/^\/api\/owner\/users\/(.+)$/))) {
      const name = decodeURIComponent(m[1]);
      if (!users[name]) return no(404, 'No such person.');
      if (method === 'DELETE') { if (name === who) return no(400, "You can't remove yourself."); delete users[name]; return ok({ ok: true }); }
      if (body.role) users[name] = body.role;
      return ok({ ok: true });
    }
    if (p === '/api/password') return ok({ result: 'Password changed (not really: this is the demo). Sign in again.' });
    return no(404, 'The demo has no ' + method + ' ' + p + '.');
  }

  const realFetch = window.fetch.bind(window);
  window.fetch = async (input, opts = {}) => {
    const path = typeof input === 'string' ? input : input.url;
    if (!path.startsWith('/api/')) return realFetch(input, opts);
    const body = opts.body ? JSON.parse(opts.body) : {};
    await new Promise(r => setTimeout(r, path === '/api/chat' ? 700 : 60));
    return route((opts.method || 'GET').toUpperCase(), path, body);
  };
})();
