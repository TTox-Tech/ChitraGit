#!/usr/bin/env python3
"""Generates the README artwork in docs/assets from the ledger palette.

Usage: python3 docs/assets/src/gen.py docs/assets
(Optional: pip install fonttools brotli, to embed only the letters each SVG uses.)
The hex values below mirror internal/ui/theme/theme.go; change both together
(see docs/brand.md). social-preview.png is a screenshot of social-preview.svg
at 1280x640, uploaded by hand in the repository settings.
"""
import os
import sys
from xml.sax.saxutils import escape

OUT = sys.argv[1]
os.makedirs(OUT, exist_ok=True)

P = {
    "dark": dict(bg="#0A0C10", surface="#111418", raised="#1A1F26", rule="#2B313A", text="#E6EDF3",
                 muted="#8B949E", accent="#3B82F6", sky="#7DD3FC", sage="#72C795", amber="#E3B341", sindoor="#F47067"),
    "light": dict(bg="#F6F8FA", surface="#EEF1F5", raised="#E4E8EE", rule="#D0D7DE", text="#0D1117",
                  muted="#57606A", accent="#1D4ED8", sky="#0E6A8C", sage="#1A7F37", amber="#9A6700", sindoor="#CF222E"),
}
# One brand font everywhere, embedded so GitHub shows the same letters on every OS:
# M PLUS Code Latin (variable, weight 100-700) for the wordmark, titles, text and code.
# Inter only fills in the few symbols it lacks (✓ ✗ ★).
FONTS = os.path.join(os.path.dirname(os.path.abspath(__file__)), "..", "fonts")
BRAND = "'M PLUS Code Latin',Inter,ui-monospace,Menlo,Consolas,monospace"
DISPLAY = SANS = MONO = BRAND
FACES = [  # (family, font-weight range, file), in fallback order
    ("M PLUS Code Latin", "100 700", "m-plus-code-latin.woff2"),
    ("Inter", "400", "inter-regular.woff2"),
]


def subsets(chars):
    """Each face cut down to the chars it supplies that earlier faces lack.
    Without fontTools: the whole brand font, and no fallback."""
    try:
        import io
        from fontTools import subset as fts
        from fontTools.ttLib import TTFont
    except ImportError:
        family, weight, name = FACES[0]
        with open(os.path.join(FONTS, name), "rb") as f:
            return [(family, weight, f.read())]
    out, left = [], set(chars) - {" "}
    for family, weight, name in FACES:
        font = TTFont(os.path.join(FONTS, name))
        mine = {ch for ch in left if ord(ch) in font.getBestCmap()}
        if not mine:
            continue
        left -= mine
        opts = fts.Options()
        opts.flavor = "woff2"
        opts.layout_features = ["kern", "liga", "calt"]
        sub = fts.Subsetter(opts)
        sub.populate(text="".join(mine) + " ")
        sub.subset(font)
        buf = io.BytesIO()
        font.flavor = "woff2"
        font.save(buf)
        out.append((family, weight, buf.getvalue()))
    return out


def font_faces(body):
    """@font-face rules, with the font data inline, for the text body uses."""
    import base64
    import html
    import re
    if "<text" not in body:
        return ""
    chars = set(html.unescape(re.sub(r"<style>.*?</style>|<[^>]+>", " ", body, flags=re.S)))
    return "\n".join(f"@font-face{{font-family:'{family}';font-weight:{weight};"
                     f"src:url(data:font/woff2;base64,{base64.b64encode(data).decode()}) format('woff2')}}"
                     for family, weight, data in subsets(chars))


def style(c, extra=""):
    return f"""<style>
.bg{{fill:{c['bg']}}} .surface{{fill:{c['surface']}}} .raised{{fill:{c['raised']}}}
.rule{{stroke:{c['rule']}}} .rulef{{fill:{c['rule']}}}
.text{{fill:{c['text']}}} .muted{{fill:{c['muted']}}} .accent{{fill:{c['accent']}}} .accents{{stroke:{c['accent']}}}
.sky{{fill:{c['sky']}}} .sage{{fill:{c['sage']}}} .amber{{fill:{c['amber']}}} .sindoor{{fill:{c['sindoor']}}}
.display{{font-family:{DISPLAY};font-weight:600}} .sans{{font-family:{SANS}}} .mono{{font-family:{MONO}}}
{extra}
</style>"""


def mark(x, y, s, c):
    """The ChitraGit mark: a ledger page whose margin is a commit graph."""
    k = s / 64
    return f"""<g transform="translate({x} {y}) scale({k})">
  <rect x="1" y="1" width="62" height="62" rx="15" fill="{P['dark']['surface']}" stroke="{c['rule'] if c is P['light'] else P['dark']['rule']}" stroke-width="1.5"/>
  <g stroke="{P['dark']['text']}" stroke-opacity=".32" stroke-width="2.4" stroke-linecap="round">
    <line x1="40" y1="19" x2="51" y2="19"/><line x1="40" y1="28" x2="51" y2="28"/>
    <line x1="40" y1="37" x2="51" y2="37"/><line x1="40" y1="46" x2="51" y2="46"/>
  </g>
  <g fill="none" stroke="{P['dark']['accent']}" stroke-width="2.6" stroke-linecap="round">
    <line x1="20" y1="12" x2="20" y2="52"/>
    <path d="M20 19 C20 27 31 25 31 32 C31 39 20 37 20 46"/>
  </g>
  <circle cx="20" cy="19" r="4.2" fill="{P['dark']['accent']}"/>
  <circle cx="31" cy="32" r="3.8" fill="{P['dark']['surface']}" stroke="{P['dark']['accent']}" stroke-width="2.4"/>
  <circle cx="20" cy="46" r="4.2" fill="{P['dark']['accent']}"/>
</g>"""


def write(name, body):
    faces = font_faces(body)
    if faces:
        body = body.replace("<style>", "<style>\n" + faces + "\n", 1)
    with open(os.path.join(OUT, name), "w") as f:
        f.write(body)


# ---------- logo ----------
write("logo.svg", f"""<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 64 64" width="64" height="64" role="img" aria-label="ChitraGit logo">
{mark(0, 0, 64, P['dark'])}
</svg>
""")

# ---------- banner ----------
LEDGER = [
    ("sage", "✓", "torvalds/linux", "412 new commits"),
    ("muted", "·", "golang/go", "up to date"),
    ("sage", "✓", "charmbracelet/bubbletea", "1 new branch"),
    ("sage", "✓", "you/dotfiles", "updated, your changes kept"),
    ("sky", "★", "someone/vanished-tool", "kept safe"),
]


def banner(mode, w=1280, h=360, social=False):
    c = P[mode]
    rules = "".join(f'<line x1="0" y1="{y}" x2="{w}" y2="{y}" class="rule" stroke-width="1" opacity=".55"/>'
                    for y in range(36, h, 36))
    cx = 96 if not social else 120
    title_y = 196 if not social else 262
    entries = ""
    ex, ey = (812, 106) if not social else (780, 214)
    for i, (cls, g, repo, note) in enumerate(LEDGER):
        yy = ey + i * 36
        entries += (f'<text x="{ex}" y="{yy}" class="mono {cls}" font-size="17">{g}</text>'
                    f'<text x="{ex+26}" y="{yy}" class="mono text" font-size="16">{escape(repo)}</text>'
                    )
    notes = "".join(
        f'<text x="{w-64}" y="{ey + i*36}" text-anchor="end" class="sans muted" font-size="14">{escape(n)}</text>'
        for i, (_, _, _, n) in enumerate(LEDGER))
    extra = ""
    if social:
        extra = f"""
  <g class="sans" font-size="20">
    <text x="{cx}" y="{title_y+128}" class="muted">Sync, browse and protect thousands of local repos</text>
    <text x="{cx}" y="{title_y+158}" class="muted">from one elegant, clickable terminal UI.</text>
  </g>
  <text x="{cx}" y="{h-56}" class="sans muted" font-size="16" letter-spacing="2">GITHUB.COM/TTOX-TECH/CHITRAGIT</text>"""
    return f"""<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 {w} {h}" width="{w}" height="{h}" role="img" aria-label="ChitraGit: the keeper of your Git archive">
{style(c)}
<defs><clipPath id="r"><rect width="{w}" height="{h}" rx="{0 if social else 18}"/></clipPath></defs>
<g clip-path="url(#r)">
  <rect width="{w}" height="{h}" class="bg"/>
  {rules}
  <line x1="{cx-40}" y1="0" x2="{cx-40}" y2="{h}" class="accents" stroke-width="1.5" opacity=".9"/>
  <line x1="{cx-34}" y1="0" x2="{cx-34}" y2="{h}" class="accents" stroke-width="1" opacity=".45"/>
  <rect x="{ex-36}" y="{ey-44}" width="{w-ex+36-36}" height="{len(LEDGER)*36+40}" rx="12" class="surface" opacity=".92"/>
  <rect x="{ex-36}" y="{ey-44}" width="{w-ex+36-36}" height="{len(LEDGER)*36+40}" rx="12" fill="none" class="rule" stroke-width="1"/>
  {entries}
  {notes}
</g>
{mark(cx, title_y-(150 if not social else 172), 64 if not social else 72, c)}
<text x="{cx}" y="{title_y}" class="display text" font-size="{78 if not social else 92}" letter-spacing="-1">Chitra<tspan class="accent">Git</tspan></text>
<text x="{cx+2}" y="{title_y+48}" class="sans muted" font-size="{26 if not social else 30}" >The keeper of your Git archive.</text>
{'' if social else f'<text x="{cx+2}" y="{title_y+112}" class="sans muted" font-size="14" letter-spacing="3">SYNC  ·  BROWSE  ·  PROTECT  ·  5,000+ REPOS  ·  OFFLINE</text>'}
{extra}
<rect x=".5" y=".5" width="{w-1}" height="{h-1}" rx="{0 if social else 18}" fill="none" class="rule" stroke-width="1"/>
</svg>
"""


write("banner-dark.svg", banner("dark"))
write("banner-light.svg", banner("light"))
write("social-preview.svg", banner("dark", 1280, 640, social=True))

# ---------- terminal demo (always the dark ledger: it is a terminal) ----------
c = P["dark"]
LINES = [
    # (class, text) pieces
    [("sage", "✓ "), ("text", "Found 1284 repos (3 new, 1 moved, 0 missing)")],
    [("sage", "✓ "), ("textb", "torvalds/linux"), ("muted", "  412 new commits, 2 new tags")],
    [("sage", "✓ "), ("textb", "golang/go"), ("muted", "  37 new commits")],
    [("sage", "✓ "), ("textb", "you/dotfiles"), ("text", "  applied 4 new commits and kept your local changes")],
    [("sage", "✓ "), ("textb", "charmbracelet/bubbletea"), ("muted", "  6 new commits, 1 new branch")],
    [("muted", "  ⋮")],
    [("amber", "! "), ("textb", "old/fork"), ("text", "  upstream rewrote its history (force-push) and this branch has local work; it was left as it was")],
    [("sindoor", "✗ "), ("textb", "acme/vanished"), ("text", "  the remote repository was not found")],
    [],
    [("amber", "41 updated · 1236 unchanged · 6 skipped · 1 failed  in 48.2s")],
]
W, LH, TOP, LEFT = 1280, 30, 112, 48
H = TOP + (len(LINES) + 2) * LH + 40
T = 16.0
HOLD_END = 14.6
CMD = "chitragit sync"
CW = 8.5  # advance of the brand font at 17px (500/1000 em)
type_start, type_end = 0.7, 1.9
times = [2.5 + i * 0.42 for i in range(len(LINES))]
final_prompt = times[-1] + 0.6


def kf(name, t0):
    a, b = t0 / T * 100, min(t0 + 0.12, HOLD_END) / T * 100
    e1, e2 = HOLD_END / T * 100, (HOLD_END + 0.5) / T * 100
    return (f"@keyframes {name}{{0%,{a:.2f}%{{opacity:0}}{b:.2f}%,{e1:.2f}%{{opacity:1}}{e2:.2f}%,100%{{opacity:0}}}}"
            f".{name}{{animation:{name} {T}s linear infinite both}}")


css = [kf("p0", 0.0)]
# typing: a cover rect slides right one character at a time
steps = len(CMD)
frames = "".join(f"{(type_start + (type_end - type_start) * i / steps) / T * 100:.2f}%{{transform:translateX({i*CW:.1f}px)}}"
                 for i in range(steps + 1))
css.append(f"@keyframes ty{{0%{{transform:translateX(0)}}{frames}100%{{transform:translateX({steps*CW:.1f}px)}}}}"
           f".ty{{animation:ty {T}s steps(1,end) infinite both}}")
css.append(kf("cmd", 0.0))
for i, t in enumerate(times):
    css.append(kf(f"l{i}", t))
css.append(kf("pf", final_prompt))
css.append("@keyframes blink{0%,49%{opacity:1}50%,100%{opacity:0}}.cur{animation:blink 1.1s steps(1,end) infinite}")
css.append(f".textb{{fill:{c['text']};font-weight:700}}")
css.append("@media (prefers-reduced-motion: reduce){.p0,.cmd,.pf,"
           + ",".join(f".l{i}" for i in range(len(LINES))) + "{animation:none;opacity:1}.ty{animation:none;transform:translateX(200px)}.cur{animation:none}}")

body = []
y = TOP
body.append(f'<g class="p0"><text x="{LEFT}" y="{y}" class="mono" font-size="17"><tspan class="muted">~/archive</tspan><tspan class="accent"> ❯ </tspan></text></g>')
cmd_x = LEFT + 12 * CW
body.append(f'<text x="{cmd_x:.1f}" y="{y}" class="mono text cmd" font-size="17">{CMD}</text>')
body.append(f'<rect x="{cmd_x-1:.1f}" y="{y-18}" width="{steps*CW+24:.1f}" height="26" class="surface ty"/>')
for i, line in enumerate(LINES):
    y += LH
    if not line:
        continue
    spans = "".join(f'<tspan class="{cls}">{escape(t)}</tspan>' for cls, t in line)
    body.append(f'<text x="{LEFT}" y="{y}" class="mono l{i}" font-size="17" xml:space="preserve">{spans}</text>')
y += LH
body.append(f'<g class="pf"><text x="{LEFT}" y="{y}" class="mono" font-size="17"><tspan class="muted">~/archive</tspan><tspan class="accent"> ❯ </tspan></text>'
            f'<rect x="{cmd_x:.1f}" y="{y-15}" width="8.5" height="19" class="accent cur"/></g>')

write("demo-sync.svg", f"""<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 {W} {H}" width="{W}" height="{H}" role="img" aria-label="Animated terminal: chitragit sync updates every repo in parallel and explains each one it leaves alone">
{style(c, ''.join(css))}
<rect x=".5" y=".5" width="{W-1}" height="{H-1}" rx="16" class="surface rule" stroke-width="1"/>
<path d="M.5 52 V16.5 a16 16 0 0 1 16-16 H{W-16.5} a16 16 0 0 1 16 16 V52 Z" class="raised"/>
<line x1="0" y1="52" x2="{W}" y2="52" class="rule" stroke-width="1"/>
<g class="rulef"><circle cx="30" cy="26" r="6.5"/><circle cx="52" cy="26" r="6.5"/><circle cx="74" cy="26" r="6.5"/></g>
<text x="{W/2}" y="31" text-anchor="middle" class="sans muted" font-size="14">chitragit sync  —  ~/archive</text>
{chr(10).join(body)}
</svg>
""")


# ---------- sync pipeline ----------
def pipeline(mode):
    c = P[mode]
    w, h = 1280, 470
    stages = [
        ("Scan", ["finds clones, mirrors,", "worktrees, nested repos"]),
        ("Preflight", ["skips repos that are busy:", "merge, rebase, index.lock"]),
        ("Fetch", ["all branches and tags,", "never prunes, retries"]),
        ("Integrate", ["fast-forward when safe,", "local changes kept"]),
        ("Record", ["every ref change,", "old and new SHA"]),
    ]
    bw, gap, top = 208, 40, 70
    x0 = (w - (len(stages) * bw + (len(stages) - 1) * gap)) / 2
    out = []
    for i, (name, lines) in enumerate(stages):
        x = x0 + i * (bw + gap)
        hl = name == "Integrate"
        out.append(f'<rect x="{x}" y="{top}" width="{bw}" height="128" rx="14" class="surface"/>'
                   f'<rect x="{x}" y="{top}" width="{bw}" height="128" rx="14" fill="none" class="{"accents" if hl else "rule"}" stroke-width="{1.6 if hl else 1}"/>'
                   f'<text x="{x+22}" y="{top+34}" class="sans muted" font-size="12" letter-spacing="2">{"0" + str(i+1)}</text>'
                   f'<text x="{x+22}" y="{top+66}" class="display text" font-size="25">{name}</text>'
                   + "".join(f'<text x="{x+22}" y="{top+92+j*19}" class="sans muted" font-size="14">{escape(l)}</text>' for j, l in enumerate(lines)))
        if i:
            ax = x - gap
            out.append(f'<line x1="{ax+6}" y1="{top+64}" x2="{x-10}" y2="{top+64}" class="rule" stroke-width="1.6"/>'
                       f'<path d="M{x-12} {top+59} l7 5 l-7 5" fill="none" class="accents" stroke-width="1.6" stroke-linecap="round" stroke-linejoin="round"/>')
    ix = x0 + 3 * (bw + gap) + bw / 2
    by = top + 128
    # two outcomes under Integrate
    lx, rx = 250, 720
    oy = 330
    out.append(f'<path d="M{ix} {by} V{by+34} H{lx+200} V{oy-34}" fill="none" class="rule" stroke-width="1.6"/>'
               f'<path d="M{ix} {by+34} H{rx+200} V{oy-34}" fill="none" class="rule" stroke-width="1.6"/>')
    out.append(f'<circle cx="{ix}" cy="{by+34}" r="4" class="accent"/>')

    def card(x, cls, glyph, title, sub):
        return (f'<rect x="{x}" y="{oy-34}" width="400" height="120" rx="14" class="surface"/>'
                f'<rect x="{x}" y="{oy-34}" width="400" height="120" rx="14" fill="none" class="rule" stroke-width="1"/>'
                f'<text x="{x+24}" y="{oy+4}" class="mono {cls}" font-size="22">{glyph}</text>'
                f'<text x="{x+56}" y="{oy+3}" class="display text" font-size="19">{escape(title)}</text>'
                + "".join(f'<text x="{x+56}" y="{oy+32+j*20}" class="sans muted" font-size="14">{escape(s)}</text>' for j, s in enumerate(sub)))
    out.append(card(lx, "sage", "✓", "Safe: move the branch ahead", ["no local commits; local changes are stashed",
                                                                       "and put back. Other branches move too."]))
    out.append(card(rx, "amber", "!", "Not safe: leave it exactly as is", ["new data is still downloaded; the reason is",
                                                                            "recorded: ahead · diverged · conflict · …"]))
    return f"""<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 {w} {h}" width="{w}" height="{h}" role="img" aria-label="Sync pipeline: scan, preflight, fetch, integrate, record. Integrate fast-forwards only when safe and keeps local changes; otherwise the repo is left as is with a recorded reason.">
{style(c)}
<rect x=".5" y=".5" width="{w-1}" height="{h-1}" rx="18" class="bg rule" stroke-width="1"/>
<text x="{x0}" y="42" class="sans muted" font-size="13" letter-spacing="3">HOW ONE REPO IS SYNCED  ·  ON A WORKER POOL, MANY AT ONCE</text>
{''.join(out)}
</svg>
"""


write("sync-pipeline-dark.svg", pipeline("dark"))
write("sync-pipeline-light.svg", pipeline("light"))


# ---------- roadmap ----------
def roadmap(mode):
    c = P[mode]
    w, h = 1280, 210
    ms = [("M0", "Foundation"), ("M1", "Engine"), ("M2", "Safety"), ("M3", "TUI core"), ("M4", "TUI depth"),
          ("M5", "Providers"), ("M6", "Conflicts"), ("M7", "Experience"), ("M8", "Ship")]
    done, now = 9, 8  # every milestone is done for v1.0
    x0, x1, y = 90, w - 90, 102
    step = (x1 - x0) / (len(ms) - 1)
    out = [f'<line x1="{x0}" y1="{y}" x2="{x1}" y2="{y}" class="rule" stroke-width="2"/>',
           f'<line x1="{x0}" y1="{y}" x2="{x0+step*now}" y2="{y}" class="accents" stroke-width="2.5"/>']
    for i, (m, name) in enumerate(ms):
        x = x0 + i * step
        if i < done:
            out.append(f'<circle cx="{x}" cy="{y}" r="9" class="accent"/>'
                       f'<path d="M{x-4} {y} l3 3 l5 -6" fill="none" stroke="{c["bg"]}" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"/>')
        elif i == now:
            out.append(f'<circle cx="{x}" cy="{y}" r="9" class="bg accents" stroke-width="2.5"/>'
                       f'<circle cx="{x}" cy="{y}" r="9" fill="none" class="accents pulse" stroke-width="2"/>'
                       f'<circle cx="{x}" cy="{y}" r="3.5" class="accent"/>')
        else:
            out.append(f'<circle cx="{x}" cy="{y}" r="7" class="bg rule" stroke-width="2"/>')
        cls = "text" if i <= now else "muted"
        out.append(f'<text x="{x}" y="{y-28}" text-anchor="middle" class="mono {"accent" if i <= now else "muted"}" font-size="14">{m}</text>'
                   f'<text x="{x}" y="{y+40}" text-anchor="middle" class="display {cls}" font-size="18">{name}</text>')
        if i == now and i >= done:
            out.append(f'<text x="{x}" y="{y+62}" text-anchor="middle" class="sans muted" font-size="12" letter-spacing="2">NEXT</text>')
    extra = (".pulse{transform-box:fill-box;transform-origin:center;animation:pulse 2.4s ease-out infinite}"
             "@keyframes pulse{0%{transform:scale(1);opacity:.9}100%{transform:scale(2.6);opacity:0}}"
             "@media (prefers-reduced-motion: reduce){.pulse{animation:none;opacity:0}}")
    return f"""<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 {w} {h}" width="{w}" height="{h}" role="img" aria-label="Roadmap: all nine milestones, M0 Foundation to M8 Ship, are done in v1.0.">
{style(c, extra)}
<rect x=".5" y=".5" width="{w-1}" height="{h-1}" rx="18" class="bg rule" stroke-width="1"/>
{''.join(out)}
</svg>
"""


write("roadmap-dark.svg", roadmap("dark"))
write("roadmap-light.svg", roadmap("light"))


# ---------- before / after ----------
BEFORE = [
    ("muted", "$ for d in */; do (cd \"$d\" && git pull); done"),
    ("text", "Updating 3f2a91c..8be04d1"),
    ("sindoor", "error: Your local changes would be overwritten by merge."),
    ("sindoor", "Aborting"),
    ("text", "CONFLICT (content): Merge conflict in src/main.go"),
    ("sindoor", "fatal: repository 'github.com/x/tool' not found"),
    ("muted", "... 463 more repos, one at a time"),
]
AFTER = [
    ("accent", "$ chitragit sync"),
    ("sage", "✓ 412 repos updated in 3m 08s, 8 at a time"),
    ("sage", "✓ your changes kept in 17 repos"),
    ("amber", "! 2 diverged: new data downloaded, nothing merged"),
    ("sky", "★ x/tool vanished upstream: kept safe"),
    ("muted", "· 49 already up to date"),
    ("muted", "0 commits lost  ·  log: .chitragit/logs/"),
]


def before_after(mode):
    c = P[mode]
    w, h = 1280, 380
    pw, top, ph = 596, 64, 292

    def pane(x, title, cls, rows, hl):
        out = [f'<rect x="{x}" y="{top}" width="{pw}" height="{ph}" rx="14" class="surface"/>',
               f'<rect x="{x}" y="{top}" width="{pw}" height="{ph}" rx="14" fill="none" class="{"accents" if hl else "rule"}" stroke-width="{1.6 if hl else 1}"/>',
               f'<line x1="{x}" y1="{top+44}" x2="{x+pw}" y2="{top+44}" class="rule" stroke-width="1"/>']
        for i, k in enumerate(["rulef", "rulef", "rulef"]):
            out.append(f'<circle cx="{x+22+i*16}" cy="{top+22}" r="5" class="{k}"/>')
        out.append(f'<text x="{x+pw-22}" y="{top+27}" text-anchor="end" class="sans {cls}" font-size="13" letter-spacing="2.5">{title}</text>')
        for i, (k, line) in enumerate(rows):
            out.append(f'<text x="{x+24}" y="{top+80+i*31}" class="mono {k}" font-size="15">{escape(line)}</text>')
        return "".join(out)

    return f"""<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 {w} {h}" width="{w}" height="{h}" role="img" aria-label="Before: a shell loop of git pull stops on local changes, conflicts and deleted repos. After: chitragit sync updates 412 repos in parallel, keeps local changes, never merges diverged repos and keeps vanished repos safe.">
{style(c)}
<rect x=".5" y=".5" width="{w-1}" height="{h-1}" rx="18" class="bg rule" stroke-width="1"/>
<text x="32" y="42" class="sans muted" font-size="13" letter-spacing="3">BEFORE  ·  A SHELL LOOP</text>
<text x="{w-32}" y="42" text-anchor="end" class="sans accent" font-size="13" letter-spacing="3">AFTER  ·  ONE COMMAND</text>
{pane(32, "FRAGILE", "sindoor", BEFORE, False)}
{pane(w-32-pw, "SAFE", "sage", AFTER, True)}
</svg>
"""


write("before-after-dark.svg", before_after("dark"))
write("before-after-light.svg", before_after("light"))


# ---------- feature grid ----------
def icon(kind):
    """24x24 line icons drawn in the accent colour."""
    s = 'fill="none" class="accents" stroke-width="1.8" stroke-linecap="round" stroke-linejoin="round"'
    return {
        "parallel": f'<g {s}><path d="M3 6h13M3 12h16M3 18h10"/><path d="M14 3l3 3-3 3M17 9l3 3-3 3M11 15l3 3-3 3"/></g>',
        "shield": f'<g {s}><path d="M12 2.5l8 3v6c0 5-3.4 8.6-8 10-4.6-1.4-8-5-8-10v-6z"/><path d="M8.5 12l2.5 2.5 4.5-5"/></g>',
        "history": f'<g {s}><path d="M3.5 12a8.5 8.5 0 1 0 2.5-6"/><path d="M3 3v4.5h4.5"/><path d="M12 7.5V12l3 2"/></g>',
        "window": f'<g {s}><rect x="2.5" y="4" width="19" height="16" rx="2.5"/><path d="M2.5 8.5h19M9 8.5V20"/></g>',
        "lock": f'<g {s}><rect x="4.5" y="10.5" width="15" height="10.5" rx="2"/><path d="M8 10.5V7.5a4 4 0 0 1 8 0v3M12 14.5v2.5"/></g>',
        "star": f'<g {s}><path d="M12 3l2.7 5.6 6.1.9-4.4 4.3 1 6.1L12 17l-5.4 2.9 1-6.1-4.4-4.3 6.1-.9z"/></g>',
        "forge": f'<g {s}><circle cx="12" cy="12" r="9"/><path d="M3 12h18M12 3c2.6 2.6 3.8 5.6 3.8 9s-1.2 6.4-3.8 9c-2.6-2.6-3.8-5.6-3.8-9S9.4 5.6 12 3z"/></g>',
        "audit": f'<g {s}><circle cx="10.5" cy="10.5" r="7"/><path d="M15.5 15.5l5.5 5.5M7.5 10.5l2 2 3.5-4"/></g>',
        "calendar": f'<g {s}><rect x="3" y="5" width="18" height="16" rx="2.5"/><path d="M3 10h18M8 3v4M16 3v4M8 14.5h3"/></g>',
    }[kind]


FEATURES = [
    ("parallel", "Parallel sync", ["5,000+ repos on a worker pool,", "per-host limits and retries"]),
    ("shield", "Never loses your work", ["local changes are stashed and put", "back; clashes are rolled back"]),
    ("history", "Never loses history", ["deleted branches and force-pushed", "history are kept as refs"]),
    ("window", "A ledger you can click", ["repo browser, README, commits,", "conflicts, health, settings"]),
    ("star", "Rescued repos", ["repos that vanish upstream get a", "badge, protection and a backup"]),
    ("forge", "Any forge", ["GitHub, GitLab, Gitea, Codeberg", "or any plain Git URL"]),
    ("audit", "KodeKavach audit", ["leaked secrets, vulnerable deps,", "risky workflows, poisoned agent files"]),
    ("lock", "Hardened git runner", ["hooks and fsmonitor never run;", "no prompt can hang a run"]),
    ("calendar", "Scheduled and offline", ["cron, launchd or Task Scheduler;", "no telemetry, ever"]),
]


def features(mode):
    c = P[mode]
    w, cols, gap, pad = 1280, 3, 20, 24
    cw, ch = (w - 2 * pad - (cols - 1) * gap) / cols, 150
    rows = (len(FEATURES) + cols - 1) // cols
    h = pad * 2 + rows * ch + (rows - 1) * gap
    out = []
    for i, (ic, title, lines) in enumerate(FEATURES):
        x = pad + (i % cols) * (cw + gap)
        y = pad + (i // cols) * (ch + gap)
        out.append(f'<rect x="{x}" y="{y}" width="{cw}" height="{ch}" rx="14" class="surface"/>'
                   f'<rect x="{x}" y="{y}" width="{cw}" height="{ch}" rx="14" fill="none" class="rule" stroke-width="1"/>'
                   f'<rect x="{x+24}" y="{y+24}" width="44" height="44" rx="11" class="raised"/>'
                   f'<g transform="translate({x+34} {y+34}) scale({24/24})">{icon(ic)}</g>'
                   f'<text x="{x+84}" y="{y+53}" class="display text" font-size="21">{escape(title)}</text>'
                   + "".join(f'<text x="{x+24}" y="{y+100+j*22}" class="sans muted" font-size="15">{escape(l)}</text>'
                             for j, l in enumerate(lines)))
    return f"""<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 {w} {h}" width="{w}" height="{h}" role="img" aria-label="Features: parallel sync, never loses your work, never loses history, a clickable ledger, rescued repos, any forge, KodeKavach security audit, hardened git runner, scheduled and offline.">
{style(c)}
{''.join(out)}
</svg>
"""


write("features-dark.svg", features("dark"))
write("features-light.svg", features("light"))


# ---------- stats strip ----------
STATS = [("5,000+", "repos in one archive"), ("0", "commits lost, by design"), ("1", "binary, only needs Git"),
         ("3", "OSes: Linux, macOS, Windows"), ("0", "telemetry, ever")]


def stats(mode):
    c = P[mode]
    w, h = 1280, 128
    sw = (w - 48) / len(STATS)
    out = []
    for i, (n, label) in enumerate(STATS):
        x = 24 + i * sw + sw / 2
        if i:
            out.append(f'<line x1="{24+i*sw}" y1="30" x2="{24+i*sw}" y2="{h-30}" class="rule" stroke-width="1"/>')
        out.append(f'<text x="{x}" y="66" text-anchor="middle" class="display {"accent" if i == 1 else "text"}" font-size="38">{n}</text>'
                   f'<text x="{x}" y="94" text-anchor="middle" class="sans muted" font-size="14">{escape(label)}</text>')
    return f"""<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 {w} {h}" width="{w}" height="{h}" role="img" aria-label="5,000+ repos in one archive. 0 commits lost, by design. 1 binary that only needs Git. Linux, macOS and Windows. No telemetry, ever.">
{style(c)}
<rect x=".5" y=".5" width="{w-1}" height="{h-1}" rx="18" class="bg rule" stroke-width="1"/>
{''.join(out)}
</svg>
"""


write("stats-dark.svg", stats("dark"))
write("stats-light.svg", stats("light"))


# ---------- palette card ----------
def palette():
    w, h = 1280, 330
    roles = [("bg", "Background"), ("surface", "Surface"), ("rule", "Rule"), ("text", "Text"), ("muted", "Muted"),
             ("accent", "Neel"), ("sky", "Aasmani"), ("sage", "Sage"), ("amber", "Amber"), ("sindoor", "Sindoor")]
    out = []
    for r, mode in enumerate(["dark", "light"]):
        c = P[mode]
        y = 20 + r * 155
        out.append(f'<rect x="20" y="{y}" width="{w-40}" height="140" rx="14" fill="{c["bg"]}" stroke="{c["rule"]}"/>')
        out.append(f'<text x="44" y="{y+34}" font-size="13" letter-spacing="3" fill="{c["muted"]}" class="sans">LEDGER · {mode.upper()}</text>')
        sw = (w - 88) / len(roles)
        for i, (k, label) in enumerate(roles):
            x = 44 + i * sw
            out.append(f'<rect x="{x}" y="{y+50}" width="{sw-14}" height="40" rx="8" fill="{c[k]}" stroke="{c["rule"]}"/>'
                       f'<text x="{x}" y="{y+110}" font-size="14" fill="{c["text"]}" class="sans">{label}</text>'
                       f'<text x="{x}" y="{y+128}" font-size="12" fill="{c["muted"]}" class="mono">{c[k]}</text>')
    return f"""<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 {w} {h}" width="{w}" height="{h}" role="img" aria-label="The ledger palette in dark and light">
<style>.sans{{font-family:{SANS}}} .mono{{font-family:{MONO}}}</style>
{''.join(out)}
</svg>
"""


write("palette.svg", palette())
print("ok")
