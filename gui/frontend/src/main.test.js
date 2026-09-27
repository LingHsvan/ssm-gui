// Factory smoke test for the SSM web GUI front-end.
//
// There is no module boundary in main.js — it wires the whole app to the DOM
// on import and talks to the backend over fetch + SSE. So this test loads the
// real index.html markup, stubs fetch / EventSource / localStorage, imports
// main.js once, then drives every interaction through the same event
// delegation the browser uses and asserts the resulting DOM / render.

import { describe, it, expect, beforeAll, vi } from 'vitest';
import htmlRaw from '../index.html?raw';
import enLocale from '../public/locales/en.json';
import zhTwLocale from '../public/locales/zh-TW.json';

// ── test doubles ────────────────────────────────────────────
let sse;                 // the EventSource main.js creates
const fetchCalls = [];   // [url, init] for every fetch

class MockEventSource {
  constructor(url) { this.url = url; this.onmessage = null; this.onerror = null; sse = this; }
  close() {}
}

const songDB = {
  songs: {
    325: {
      musicTitle: ['EXIST', 'EXIST'],
      difficulty: { 0: { playLevel: 9 }, 1: { playLevel: 14 }, 2: { playLevel: 20 }, 3: { playLevel: 26 } },
      bandId: 1,
      jacketImage: ['exist'],
    },
    // Search-glyph-fold fixtures: a shinjitai title and a katakana title.
    773: {
      musicTitle: ['かぼーんっと極楽☆湯～とぴあ！', 'かぼーんっと極樂☆湯～とぴあ！'],
      difficulty: { 0: { playLevel: 5 }, 1: { playLevel: 12 } },
      bandId: 1,
      jacketImage: ['kabon'],
    },
    77: {
      musicTitle: ['ロミオ', 'ロミオ'],
      difficulty: { 0: { playLevel: 8 }, 1: { playLevel: 15 } },
      bandId: 1,
      jacketImage: ['romeo'],
    },
  },
  bands: { 1: { bandName: ['バンド', 'Band', '樂團', '乐团', ''] } },
};

const devices = { TESTSERIAL: { width: 1080, height: 2340 } };

// Mutable per-test responses for the detect endpoints.
let detectAdbResp = { serial: 'TESTSERIAL' };
let detectSongResp = { matched: false, candidates: [] };

function jsonResp(obj) {
  return Promise.resolve({
    ok: true,
    status: 200,
    json: () => Promise.resolve(obj),
    text: () => Promise.resolve(JSON.stringify(obj)),
  });
}

function mockFetch(url, init) {
  fetchCalls.push([String(url), init]);
  const u = String(url);
  if (u.includes('/locales/zh-TW.json')) return jsonResp(zhTwLocale);
  if (u.includes('/locales/')) return jsonResp(enLocale);
  if (u.includes('/api/device')) return jsonResp(devices);
  if (u.includes('/api/songdb')) return jsonResp(songDB);
  if (u.includes('/api/detect-adb')) return jsonResp(detectAdbResp);
  if (u.includes('/api/detect-song')) return jsonResp(detectSongResp);
  return jsonResp({}); // run / start / stop / offset / extract / kill-adb
}

const flush = (ms = 0) => new Promise((r) => setTimeout(r, ms));
const click = (sel) => document.querySelector(sel).click();
const setInput = (sel, val) => {
  const el = document.querySelector(sel);
  el.value = val;
  el.dispatchEvent(new window.Event('input', { bubbles: true }));
  return el;
};

beforeAll(async () => {
  // Real markup from index.html (scripts in innerHTML stay inert).
  const doc = new DOMParser().parseFromString(htmlRaw, 'text/html');
  document.documentElement.innerHTML = doc.documentElement.innerHTML;

  // jsdom implements no scrolling; main.js calls scrollIntoView in a couple
  // of delayed UI niceties (Settings redirect, search focus). Stub it so
  // those timers cannot throw uncaught TypeErrors into later tests.
  window.Element.prototype.scrollIntoView = function () {};

  globalThis.EventSource = MockEventSource;
  globalThis.fetch = vi.fn(mockFetch);

  // Import once; this runs all of main.js's init side effects.
  await import('./main.js');
  await flush();   // let I18n.init() + loadDevices() fetches resolve
  await flush();
});

describe('init', () => {
  it('opens an SSE connection and loads i18n + devices', () => {
    expect(sse).toBeTruthy();
    expect(sse.url).toBe('/api/events');
    expect(fetchCalls.some(([u]) => u.includes('/api/device'))).toBe(true);
    // [data-i18n] nodes were translated from the loaded locale
    expect(document.querySelector('#nav-song [data-i18n]').textContent).toBe(enLocale['nav.song']);
  });

  it('renders the saved device list with a delete control', () => {
    const row = document.querySelector('#dev-list .dev-row');
    expect(row).toBeTruthy();
    expect(row.textContent).toContain('TESTSERIAL');
    expect(document.querySelector('#dev-list [data-action="deleteDevice"]').dataset.serial).toBe('TESTSERIAL');
  });
});

describe('navigation', () => {
  it('switches panes via delegation', () => {
    for (const id of ['play', 'settings', 'extract', 'song']) {
      click(`#nav-${id}`);
      expect(document.getElementById(`pane-${id}`).classList.contains('active')).toBe(true);
    }
  });
});

describe('song setup controls', () => {
  it('toggles game mode and reveals the APPEND difficulty for pjsk', () => {
    click('[data-action="setMode"][data-arg="pjsk"]');
    expect(document.getElementById('mode-pjsk').classList.contains('active')).toBe(true);
    expect(document.querySelectorAll('.db')[5].style.display).not.toBe('none');
    click('[data-action="setMode"][data-arg="bang"]');
    expect(document.querySelectorAll('.db')[5].style.display).toBe('none');
  });

  it('switches backend and shows the HID warning only for HID', () => {
    click('[data-action="setBackend"][data-arg="hid"]');
    expect(document.getElementById('hid-warn-box').classList.contains('hidden')).toBe(false);
    click('[data-action="setBackend"][data-arg="adb"]');
    expect(document.getElementById('hid-warn-box').classList.contains('hidden')).toBe(true);
  });

  it('selects a difficulty', () => {
    click('.db[data-arg="2"]');
    const active = [...document.querySelectorAll('.db')].findIndex((b) => b.classList.contains('active'));
    expect(active).toBe(2);
  });

  it('sets orientation', () => {
    click('[data-action="setOrient"][data-arg="right"]');
    expect(document.getElementById('or').classList.contains('active')).toBe(true);
  });
});

describe('humanization + advanced sliders', () => {
  it('renders jitter values and OFF state', () => {
    setInput('#sld-timing', '20');
    expect(document.getElementById('val-timing').textContent).toBe('±20 ms');
    setInput('#sld-timing', '0');
    expect(document.getElementById('val-timing').textContent).toBe('OFF');
    setInput('#sld-position', '3');
    expect(document.getElementById('val-position').textContent).toMatch(/±\d+%/);
  });

  it('renders advanced VTE params with their formatting', () => {
    setInput('#sld-flickFactor', '25');
    expect(document.getElementById('val-flickFactor').textContent).toBe('0.25');
    setInput('#sld-flickPow', '15');
    expect(document.getElementById('val-flickPow').textContent).toBe('1.5');
  });
});

describe('search + song selection', () => {
  it('surfaces an error in the dropdown when the song DB fails to load', async () => {
    // Runs before the DB is cached: fail the next songdb fetch once.
    globalThis.fetch.mockImplementationOnce(() =>
      Promise.resolve({ ok: false, status: 500, json: () => Promise.resolve({}), text: () => Promise.resolve('') }));
    setInput('#q', 'exist');
    await flush(260);
    expect(document.getElementById('drop').textContent).toContain(enLocale['drop.error']);
  });

  it('shows matching results in the dropdown', async () => {
    setInput('#q', 'exist');
    await flush(220); // debounce + songdb fetch (now succeeds, caches S.db)
    const items = document.querySelectorAll('#drop .di');
    expect(items.length).toBeGreaterThan(0);
    expect(document.querySelector('#drop .di-title').textContent).toBe('EXIST');
  });

  it('selects a song from the dropdown', () => {
    document.querySelector('#drop .di[data-action="selSong"]').click();
    expect(document.getElementById('sb-id').textContent).toBe('#325');
    expect(document.getElementById('song-id').value).toBe('325');
    expect(document.getElementById('sel-bar').classList.contains('show')).toBe(true);
  });
});

describe('search glyph folding (simplified / lookalike input)', () => {
  it('finds the shinjitai title from simplified input (极乐 -> 極楽)', async () => {
    setInput('#q', '极乐');
    await flush(220); // debounce + cached DB
    const row = document.querySelector('#drop .di[data-action="selSong"][data-arg="773"]');
    expect(row).toBeTruthy();
    expect(row.textContent).toContain('かぼーんっと');
  });

  it('finds the katakana title from lookalike kanji input (口三才 -> ロミオ)', async () => {
    setInput('#q', '口三才');
    await flush(220);
    const row = document.querySelector('#drop .di[data-action="selSong"][data-arg="77"]');
    expect(row).toBeTruthy();
    expect(row.textContent).toContain('ロミオ');
  });

  it('still finds the katakana title from the original katakana input', async () => {
    setInput('#q', 'ロミオ');
    await flush(220);
    const row = document.querySelector('#drop .di[data-action="selSong"][data-arg="77"]');
    expect(row).toBeTruthy();
  });
});

describe('SSE → now-playing render', () => {
  const np = { songId: 325, title: 'EXIST', artist: 'Band', diff: 'expert', diffLevel: 26, jacketUrl: 'http://x/j.png' };

  it('renders both the sidebar and play-deck cards from one message', () => {
    sse.onmessage({ data: JSON.stringify({ state: 2, offset: 7, nowPlaying: np, greatReq: 0, greatApply: 0 }) });
    expect(document.getElementById('np-card').style.display).toBe('block');
    expect(document.getElementById('np-title').textContent).toBe('EXIST');
    expect(document.getElementById('pn-title-big').textContent).toBe('EXIST');
    expect(document.getElementById('pn-diff-badge').textContent).toBe('EXPERT');
    expect(document.getElementById('ov').textContent).toBe('7');
    expect(document.getElementById('pn-dot').className).toContain('playing');
  });

  it('skips re-painting the jacket when the payload is unchanged', () => {
    const before = document.getElementById('pn-img').getAttribute('src');
    document.getElementById('pn-img').setAttribute('src', 'SENTINEL');
    sse.onmessage({ data: JSON.stringify({ state: 2, offset: 9, nowPlaying: np }) });
    // unchanged nowPlaying → renderNowPlaying is a no-op, src not reset
    expect(document.getElementById('pn-img').getAttribute('src')).toBe('SENTINEL');
    expect(document.getElementById('ov').textContent).toBe('9'); // offset still updates
    document.getElementById('pn-img').setAttribute('src', before || '');
  });

  it('ignores malformed SSE frames without throwing', () => {
    expect(() => sse.onmessage({ data: '{bad json' })).not.toThrow();
  });
});

describe('restart flow (re-arms the same song, no re-load)', () => {
  const np = { songId: 325, title: 'EXIST', artist: 'Band', diff: 'expert', diffLevel: 26, jacketUrl: 'http://x/j.png' };

  it('clicking Restart posts /api/restart', async () => {
    sse.onmessage({ data: JSON.stringify({ state: 2, offset: 0, nowPlaying: np }) }); // playing
    expect(document.getElementById('pn-loaded').style.display).toBe('block');
    fetchCalls.length = 0;
    document.querySelector('[data-action="apiRestart"]').click();
    await flush();
    expect(fetchCalls.some(([u]) => u.includes('/api/restart'))).toBe(true);
  });

  it('keeps the song on the deck through the brief idle while re-arming', () => {
    // Backend echoes the last nowPlaying in the idle frame during re-arm; the
    // deck must stay put (no clear / flicker / re-load).
    sse.onmessage({ data: JSON.stringify({ state: 0, offset: 0, nowPlaying: np }) });
    expect(document.getElementById('pn-loaded').style.display).toBe('block');
    expect(document.getElementById('pn-title-big').textContent).toBe('EXIST');
  });

  it('returns to Ready so Start is enabled again without re-loading', () => {
    sse.onmessage({ data: JSON.stringify({ state: 1, offset: 0, nowPlaying: np }) });
    expect(document.getElementById('btn-start').disabled).toBe(false);
    expect(document.getElementById('pn-loaded').style.display).toBe('block');
  });
});

describe('API actions', () => {
  it('submits a run for the selected, configured device', async () => {
    setInput('#dev-serial', 'TESTSERIAL');
    fetchCalls.length = 0;
    click('[data-action="submitRun"]');
    await flush();
    const run = fetchCalls.find(([u]) => u.includes('/api/run'));
    expect(run).toBeTruthy();
    const body = JSON.parse(run[1].body);
    expect(body.songId).toBe(325);
    expect(body.deviceSerial).toBe('TESTSERIAL');
  });

  it('saves and deletes a device', async () => {
    setInput('#dc-s', 'NEWDEV'); setInput('#dc-w', '1080'); setInput('#dc-h', '2400');
    fetchCalls.length = 0;
    click('[data-action="saveDevice"]');
    await flush();
    const post = fetchCalls.find(([u, i]) => u.includes('/api/device') && i && i.method === 'POST');
    expect(JSON.parse(post[1].body)).toMatchObject({ serial: 'NEWDEV', width: 1080, height: 2400 });

    fetchCalls.length = 0;
    document.querySelector('#dev-list [data-action="deleteDevice"]').click();
    await flush();
    const del = fetchCalls.find(([u, i]) => u.includes('/api/device') && i && i.method === 'DELETE');
    expect(del).toBeTruthy();
  });

  it('sends offset adjustments (debounced)', async () => {
    fetchCalls.length = 0;
    click('[data-action="adj"][data-arg="50"]');
    await flush(80);
    const off = fetchCalls.find(([u]) => u.includes('/api/offset'));
    expect(JSON.parse(off[1].body).delta).toBe(50);
  });

  it('triggers extraction', async () => {
    setInput('#ex-path', './gamedata');
    fetchCalls.length = 0;
    click('[data-action="doExtract"]');
    await flush();
    expect(fetchCalls.some(([u]) => u.includes('/api/extract'))).toBe(true);
  });
});

describe('theme + i18n', () => {
  it('toggles the theme', () => {
    const before = document.documentElement.getAttribute('data-theme');
    click('[data-action="toggleTheme"]');
    expect(document.documentElement.getAttribute('data-theme')).not.toBe(before);
  });

  it('switches language through the menu', async () => {
    click('[data-action="toggleLangMenu"]');
    document.querySelector('.lang-opt[data-arg="zh-TW"]').click();
    await flush(); await flush();
    expect(document.documentElement.lang).toBe('zh-TW');
    expect(document.querySelector('#nav-song [data-i18n]').textContent).toBe(zhTwLocale['nav.song']);
  });
});

describe('log box', () => {
  it('caps entries at 200', () => {
    const box = document.getElementById('play-log');
    for (let i = 0; i < 260; i++) {
      sse.onmessage({ data: JSON.stringify({ state: 1, offset: 0, greatReq: i, greatApply: i }) });
    }
    expect(box.childElementCount).toBeLessThanOrEqual(200);
  });
});

describe('manual song id', () => {
  it('resolves a known id against the loaded DB (title + difficulty availability)', () => {
    // DB is cached from the search tests; id 325 is known with diffs 0-3.
    setInput('#song-id', '325');
    expect(document.getElementById('sel-bar').classList.contains('show')).toBe(true);
    expect(document.getElementById('sb-id').textContent).toBe('#325');
    expect(document.getElementById('sb-title').textContent).toBe('EXIST'); // real title, not "Manual input"
    expect(document.querySelectorAll('.db')[4].classList.contains('dis')).toBe(true); // SPECIAL unavailable
  });

  it('clears the selection when the id field is emptied', () => {
    setInput('#song-id', '');
    expect(document.getElementById('sel-bar').classList.contains('show')).toBe(false);
  });
});

describe('OCR candidates + device auto-pick', () => {
  it('renders fuzzy OCR candidates and selecting one sets the song', async () => {
    detectSongResp = {
      matched: false,
      candidates: [
        { songId: 325, title: 'EXIST', score: 62 },
        { songId: 999, title: '上海ハニー', score: 60 },
      ],
    };
    click('[data-action="detectSong"]');
    await flush(); await flush();
    const items = document.querySelectorAll('#det-cand .di');
    expect(items.length).toBe(2);
    expect(document.querySelector('#det-cand .di-title').textContent).toBe('EXIST');
    expect(document.getElementById('det-cand').classList.contains('open')).toBe(true);
    // i18n is still zh-TW from the language-switch test above.
    expect(document.getElementById('song-log').textContent).toContain(zhTwLocale['log.detect.candidates']);

    items[0].click();
    await flush();
    expect(document.getElementById('song-id').value).toBe('325');
    expect(document.getElementById('sb-id').textContent).toBe('#325');
    expect(document.getElementById('det-cand').classList.contains('open')).toBe(false);
  });

  it('with >=2 candidates, clicking the second row selects that candidate (QA pick-coverage)', async () => {
    detectSongResp = {
      matched: false,
      candidates: [
        { songId: 325, title: 'EXIST', score: 62 },
        { songId: 77, title: 'ロミオ', score: 60 },
      ],
    };
    click('[data-action="detectSong"]');
    await flush(); await flush();
    const items = document.querySelectorAll('#det-cand .di');
    expect(items.length).toBe(2);
    items[1].click(); // exercise the delegation for a non-first row
    await flush();
    expect(document.getElementById('song-id').value).toBe('77');
    expect(document.getElementById('sb-title').textContent).toBe('ロミオ');
    expect(document.getElementById('det-cand').classList.contains('open')).toBe(false);
  });

  it('submits an empty serial when the field is blank (backend auto-picks)', async () => {
    setInput('#song-id', '325');
    setInput('#dev-serial', '');
    fetchCalls.length = 0;
    click('[data-action="submitRun"]');
    await flush();
    const run = fetchCalls.find(([u]) => u.includes('/api/run'));
    expect(run).toBeTruthy();
    expect(JSON.parse(run[1].body).deviceSerial).toBe('');
    expect(document.getElementById('song-log').textContent).toContain(zhTwLocale['log.serial.autopick']);
  });

  it('auto-detect fills the serial, logs the saved device and refreshes the list', async () => {
    detectAdbResp = { serial: 'TESTSERIAL', width: 1080, height: 2340, saved: 'added', seen: 1 };
    fetchCalls.length = 0;
    click('[data-action="autoDetectDevice"]');
    await flush(); await flush();
    expect(document.getElementById('dev-serial').value).toBe('TESTSERIAL');
    expect(document.getElementById('song-log').textContent).toContain(zhTwLocale['log.detect.devadded']);
    expect(fetchCalls.some(([u]) => u.includes('/api/device'))).toBe(true); // loadDevices() re-ran
  });

  it('auto-detect aborts a hung backend after 20s and restores the placeholder (QA timeout path)', async () => {
    vi.useFakeTimers();
    try {
      let seenSignal = null;
      globalThis.fetch.mockImplementationOnce((url, init) => {
        seenSignal = init && init.signal;
        return new Promise((_resolve, reject) => {
          if (seenSignal) seenSignal.addEventListener('abort', () => reject(new Error('aborted by test')));
        });
      });
      const dsInput = document.getElementById('dev-serial');
      click('[data-action="autoDetectDevice"]');
      expect(dsInput.placeholder).toBe(zhTwLocale['log.detect.detecting']);
      expect(seenSignal).toBeTruthy();
      expect(seenSignal.aborted).toBe(false);

      await vi.advanceTimersByTimeAsync(20000);
      await Promise.resolve(); await Promise.resolve();

      expect(seenSignal.aborted).toBe(true);
      expect(dsInput.placeholder).toBe(''); // "detecting…" must not stick forever
      expect(document.getElementById('song-log').textContent).toContain(zhTwLocale['log.detect.fail']);
    } finally {
      vi.useRealTimers();
    }
  });

  it('still guides to Settings when an unregistered serial is typed', async () => {
    setInput('#dev-serial', 'UNKNOWN');
    fetchCalls.length = 0;
    click('[data-action="submitRun"]');
    await flush();
    expect(fetchCalls.some(([u]) => u.includes('/api/run'))).toBe(false);
    expect(document.getElementById('pane-settings').classList.contains('active')).toBe(true);
    expect(document.getElementById('song-log').textContent)
      .toContain(zhTwLocale['log.serial.unconfigured.pre'] + 'UNKNOWN');
  });
});

describe('OCR candidate picker lifecycle (QA edge cases)', () => {
  const candResp = (candidates) => ({ matched: false, candidates });

  it('closes the candidate list on Escape and on mode switch', async () => {
    // Two candidates so the picker actually opens — a single candidate is
    // auto-applied by design (see the single-candidate describe below).
    detectSongResp = candResp([
      { songId: 325, title: 'EXIST', score: 62 },
      { songId: 999, title: '上海ハニー', score: 60 },
    ]);
    const dd = document.getElementById('det-cand');

    click('[data-action="detectSong"]');
    await flush(); await flush();
    expect(dd.classList.contains('open')).toBe(true);

    // Dispatch on an element (body), like a real keydown, so it bubbles to
    // the document-level listeners with an Element target.
    document.body.dispatchEvent(new window.KeyboardEvent('keydown', { key: 'Escape', bubbles: true }));
    expect(dd.classList.contains('open')).toBe(false);

    click('[data-action="detectSong"]');
    await flush(); await flush();
    expect(dd.classList.contains('open')).toBe(true);

    click('[data-action="setMode"][data-arg="pjsk"]');
    expect(dd.classList.contains('open')).toBe(false);
    click('[data-action="setMode"][data-arg="bang"]'); // restore mode for later tests

    // A click outside the .sw wrapper closes it as well.
    click('[data-action="detectSong"]');
    await flush(); await flush();
    expect(dd.classList.contains('open')).toBe(true);
    click('#nav-song'); // any element outside the candidate wrapper
    expect(dd.classList.contains('open')).toBe(false);
  });

  it('logs the nomatch OCR text when the backend returns no candidates', async () => {
    // First open the list (two candidates — a single one would auto-apply),
    // then re-detect with an empty result: the picker must close and the
    // nomatch log must appear.
    detectSongResp = candResp([
      { songId: 325, title: 'EXIST', score: 62 },
      { songId: 999, title: '上海ハニー', score: 60 },
    ]);
    click('[data-action="detectSong"]');
    await flush(); await flush();
    expect(document.getElementById('det-cand').classList.contains('open')).toBe(true);

    detectSongResp = { matched: false, candidates: [], texts: ['noise A', 'noise B'] };
    click('[data-action="detectSong"]');
    await flush(); await flush();
    expect(document.getElementById('det-cand').classList.contains('open')).toBe(false);
    const log = document.getElementById('song-log').textContent;
    expect(log).toContain(zhTwLocale['log.detect.nomatch']);
    expect(log).toContain('noise A / noise B');
  });

  it('a candidate click resolves id, selection bar and difficulty availability', async () => {
    detectSongResp = candResp([
      { songId: 325, title: 'EXIST', score: 84 },
      { songId: 999, title: '上海ハニー', score: 60 },
    ]);
    click('[data-action="detectSong"]');
    await flush(); await flush();
    const rows = document.querySelectorAll('#det-cand .di');
    expect(rows.length).toBe(2);
    expect(rows[0].textContent).toContain('84');

    rows[0].click();
    await flush();
    expect(document.getElementById('song-id').value).toBe('325');
    expect(document.getElementById('sb-id').textContent).toBe('#325');
    expect(document.getElementById('sb-title').textContent).toBe('EXIST');
    expect(document.getElementById('sel-bar').classList.contains('show')).toBe(true);
    // Song 325 has difficulties 0-3: SPECIAL (4) disabled, EXPERT (3) available.
    expect(document.querySelectorAll('.db')[4].classList.contains('dis')).toBe(true);
    expect(document.querySelectorAll('.db')[3].classList.contains('dis')).toBe(false);
    expect(document.getElementById('det-cand').classList.contains('open')).toBe(false);
  });

  it('auto-detect: updated logs and refreshes; empty result logs none and skips refresh', async () => {
    detectAdbResp = { serial: 'TESTSERIAL', width: 1080, height: 2340, saved: 'updated', seen: 1 };
    fetchCalls.length = 0;
    click('[data-action="autoDetectDevice"]');
    await flush(); await flush();
    expect(document.getElementById('song-log').textContent).toContain(zhTwLocale['log.detect.devupdated']);
    expect(fetchCalls.some(([u]) => u.includes('/api/device'))).toBe(true); // loadDevices() re-ran

    detectAdbResp = { serial: '', seen: 0 };
    fetchCalls.length = 0;
    click('[data-action="autoDetectDevice"]');
    await flush(); await flush();
    expect(document.getElementById('song-log').textContent).toContain(zhTwLocale['log.detect.none']);
    expect(document.getElementById('dev-serial').placeholder).toBe('');
    expect(fetchCalls.some(([u]) => u.includes('/api/device'))).toBe(false); // nothing to refresh
  });

  it('blank serial with zero saved devices redirects to Settings with the required log', async () => {
    // The next loadDevices() (triggered by nav) sees an empty device map.
    globalThis.fetch.mockImplementationOnce(() => jsonResp({}));
    click('#nav-settings');
    await flush();

    setInput('#song-id', '325');
    setInput('#dev-serial', '');
    fetchCalls.length = 0;
    click('[data-action="submitRun"]');
    await flush();
    expect(fetchCalls.some(([u]) => u.includes('/api/run'))).toBe(false);
    expect(document.getElementById('pane-settings').classList.contains('active')).toBe(true);
    expect(document.getElementById('song-log').textContent).toContain(zhTwLocale['log.serial.required']);
    // The redirect itself re-runs loadDevices() with the normal mock, which
    // restores the saved device list for the drawer test below.
    await flush();
  });

  it('detect-song HTTP failure logs the error and leaves the picker closed', async () => {
    globalThis.fetch.mockImplementationOnce(() =>
      Promise.resolve({ ok: false, status: 500, json: () => Promise.resolve({}), text: () => Promise.resolve('kaboom') }));
    click('[data-action="detectSong"]');
    await flush(); await flush();
    const log = document.getElementById('song-log').textContent;
    expect(log).toContain(zhTwLocale['log.detect.fail']);
    expect(log).toContain('kaboom');
    expect(document.getElementById('det-cand').classList.contains('open')).toBe(false);
  });

  it('a backend rejection of the empty-serial run surfaces guidance in the log', async () => {
    setInput('#song-id', '325');
    setInput('#dev-serial', '');
    globalThis.fetch.mockImplementationOnce(() =>
      Promise.resolve({
        ok: false, status: 409, json: () => Promise.resolve({}),
        text: () => Promise.resolve('no registered ADB device found; add one via Auto Detect or in Settings first'),
      }));
    click('[data-action="submitRun"]');
    await flush();
    const log = document.getElementById('song-log').textContent;
    expect(log).toContain(zhTwLocale['log.serial.autopick']);
    expect(log).toContain('no registered ADB device found');
  });

  it('typing in the search box closes a stale OCR candidate picker', async () => {
    detectSongResp = candResp([
      { songId: 325, title: 'EXIST', score: 62 },
      { songId: 999, title: '上海ハニー', score: 60 },
    ]);
    click('[data-action="detectSong"]');
    await flush(); await flush();
    expect(document.getElementById('det-cand').classList.contains('open')).toBe(true);

    setInput('#q', 'exist');
    expect(document.getElementById('det-cand').classList.contains('open')).toBe(false);
    await flush(220); // let the debounced search settle
  });
});

describe('OCR single-candidate auto-apply (bugfix: no 1-entry picker)', () => {
  it('auto-applies the only candidate without opening the picker', async () => {
    setInput('#song-id', ''); // reset so the sel-bar assertion below is meaningful
    expect(document.getElementById('sel-bar').classList.contains('show')).toBe(false);

    detectSongResp = {
      matched: false,
      candidates: [{ songId: 77, title: 'ロメオ', score: 62 }],
      texts: ['ロメ才'],
    };
    click('[data-action="detectSong"]');
    await flush(); await flush();

    // The id lands in the field and the selection bar resolves via onManualId.
    expect(document.getElementById('song-id').value).toBe('77');
    expect(document.getElementById('sel-bar').classList.contains('show')).toBe(true);
    expect(document.getElementById('sb-id').textContent).toBe('#77');
    // No single-entry picker: the dropdown must stay closed.
    expect(document.getElementById('det-cand').classList.contains('open')).toBe(false);
    // i18n is still zh-TW from the language-switch test above.
    const log = document.getElementById('song-log').textContent;
    expect(log).toContain(zhTwLocale['log.detect.candone']);
    expect(log).toContain('#77 ロメオ (62, OCR');
  });

  it('a single DB-known candidate also resolves the real title and difficulties', async () => {
    detectSongResp = { matched: false, candidates: [{ songId: 325, title: 'EXIST', score: 71 }] };
    click('[data-action="detectSong"]');
    await flush(); await flush();
    expect(document.getElementById('song-id').value).toBe('325');
    expect(document.getElementById('sb-title').textContent).toBe('EXIST'); // real title, not "Manual input"
    expect(document.querySelectorAll('.db')[4].classList.contains('dis')).toBe(true); // SPECIAL unavailable
    expect(document.getElementById('det-cand').classList.contains('open')).toBe(false);
  });

  it('missing candidates field still logs nomatch and keeps the picker closed', async () => {
    detectSongResp = { matched: false, texts: ['noise C'] }; // no candidates key at all
    click('[data-action="detectSong"]');
    await flush(); await flush();
    expect(document.getElementById('det-cand').classList.contains('open')).toBe(false);
    expect(document.getElementById('song-log').textContent).toContain(zhTwLocale['log.detect.nomatch']);
  });
});

describe('regression smoke (search / device drawer / detPreview)', () => {
  it('detPreview shows the top-3 fuzzy candidates when unmatched', async () => {
    detectSongResp = {
      matched: false,
      candidates: [
        { songId: 11, title: 'A1', score: 92 },
        { songId: 12, title: 'A2', score: 84 },
        { songId: 13, title: 'A3', score: 74 },
        { songId: 14, title: 'A4', score: 60 },
      ],
      texts: ['ocr text'],
    };
    click('[data-action="detectPreview"]');
    await flush(); await flush();
    const info = document.getElementById('det-info').textContent;
    expect(info).toContain('#11 A1 (92)');
    expect(info).toContain('#13 A3 (74)');
    expect(info).not.toContain('#14'); // only the top 3 are previewed
    expect(info).toContain('ocr text');
  });

  it('device drawer still lists the saved devices', async () => {
    document.getElementById('btn-dev-drop').click();
    await flush();
    const dd = document.getElementById('dev-drop');
    expect(dd.classList.contains('open')).toBe(true);
    expect(dd.querySelector('.di-id').textContent).toContain('TESTSERIAL');
  });

  it('song search dropdown still selects after the candidate flow', async () => {
    setInput('#song-id', '');
    setInput('#q', 'exist');
    await flush(220);
    const items = document.querySelectorAll('#drop .di');
    expect(items.length).toBeGreaterThan(0);
    items[0].click();
    expect(document.getElementById('song-id').value).toBe('325');
    expect(document.getElementById('sb-title').textContent).toBe('EXIST');
  });
});
