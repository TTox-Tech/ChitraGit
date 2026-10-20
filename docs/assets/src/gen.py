#!/usr/bin/env python3
"""Generates the README artwork in docs/assets from the ledger palette.

Usage: python3 docs/assets/src/gen.py docs/assets
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
    "dark": dict(bg="#16120E", surface="#1F1A14", raised="#2A231C", rule="#3D342A", text="#EFE6D6",
                 muted="#A39684", accent="#E58A45", brass="#CDB27A", sage="#8DB58A", amber="#DDA654", sindoor="#E07A66"),
    "light": dict(bg="#FBF8F2", surface="#F4EEE3", raised="#EDE5D6", rule="#E2D8C6", text="#2B231B",
                  muted="#6E6355", accent="#B0531A", brass="#76612F", sage="#46704A", amber="#8C5B0E", sindoor="#A63D30"),
}
SERIF = "'Iowan Old Style','Palatino Linotype',Palatino,'Book Antiqua',Georgia,serif"
SANS = "-apple-system,BlinkMacSystemFont,'Segoe UI',Inter,'Helvetica Neue',Arial,sans-serif"
MONO = "ui-monospace,SFMono-Regular,'SF Mono',Menlo,Consolas,'DejaVu Sans Mono','Liberation Mono',monospace"


def style(c, extra=""):
    return f"""<style>
.bg{{fill:{c['bg']}}} .surface{{fill:{c['surface']}}} .raised{{fill:{c['raised']}}}
.rule{{stroke:{c['rule']}}} .rulef{{fill:{c['rule']}}}
.text{{fill:{c['text']}}} .muted{{fill:{c['muted']}}} .accent{{fill:{c['accent']}}} .accents{{stroke:{c['accent']}}}
.brass{{fill:{c['brass']}}} .sage{{fill:{c['sage']}}} .amber{{fill:{c['amber']}}} .sindoor{{fill:{c['sindoor']}}}
.serif{{font-family:{SERIF}}} .sans{{font-family:{SANS}}} .mono{{font-family:{MONO}}}
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
    ("amber", "!", "you/dotfiles", "uncommitted, left as is"),
    ("brass", "★", "someone/vanished-tool", "kept safe"),
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
<text x="{cx}" y="{title_y}" class="serif text" font-size="{78 if not social else 92}" letter-spacing="-1">Chitra<tspan class="accent">Git</tspan></text>
<text x="{cx+2}" y="{title_y+48}" class="serif muted" font-size="{26 if not social else 30}" font-style="italic">The keeper of your Git archive.</text>
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
    [("amber", "! "), ("textb", "you/dotfiles"), ("text", "  has uncommitted changes; downloaded 4 new commits but did not apply them")],
    [("sage", "✓ "), ("textb", "charmbracelet/bubbletea"), ("muted", "  6 new commits, 1 new branch")],
    [("muted", "  ⋮")],
    [("amber", "! "), ("textb", "old/fork"), ("text", "  upstream rewrote its history (force-push); your branch was left as it was")],
    [("sindoor", "✗ "), ("textb", "acme/vanished"), ("text", "  the remote repository was not found")],
    [],
    [("amber", "41 updated · 1236 unchanged · 6 skipped · 1 failed  in 48.2s")],
]
W, LH, TOP, LEFT = 1280, 30, 112, 48
H = TOP + (len(LINES) + 2) * LH + 40
T = 16.0
HOLD_END = 14.6
CMD = "chitragit sync"
CW = 10.25  # approx advance of the mono font at 17px
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
            f'<rect x="{cmd_x:.1f}" y="{y-15}" width="10" height="19" class="accent cur"/></g>')

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
        ("Integrate", ["fast-forward only,", "and only when safe"]),
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
                   f'<text x="{x+22}" y="{top+66}" class="serif text" font-size="25">{name}</text>'
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
                f'<text x="{x+56}" y="{oy+3}" class="serif text" font-size="21">{escape(title)}</text>'
                + "".join(f'<text x="{x+56}" y="{oy+32+j*20}" class="sans muted" font-size="14">{escape(s)}</text>' for j, s in enumerate(sub)))
    out.append(card(lx, "sage", "✓", "Safe: move the branch ahead", ["clean tree, no local commits, history intact.",
                                                                       "Branches not checked out move too, atomically."]))
    out.append(card(rx, "amber", "!", "Not safe: leave it exactly as is", ["new data is still downloaded; the reason is",
                                                                            "recorded: dirty · ahead · diverged · rewritten · …"]))
    return f"""<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 {w} {h}" width="{w}" height="{h}" role="img" aria-label="Sync pipeline: scan, preflight, fetch, integrate, record. Integrate fast-forwards only when safe; otherwise the repo is left as is with a recorded reason.">
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
    done, now = 2, 2
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
                   f'<text x="{x}" y="{y+40}" text-anchor="middle" class="serif {cls}" font-size="18">{name}</text>')
        if i == now:
            out.append(f'<text x="{x}" y="{y+62}" text-anchor="middle" class="sans muted" font-size="12" letter-spacing="2">BUILDING NOW</text>')
    extra = (".pulse{transform-box:fill-box;transform-origin:center;animation:pulse 2.4s ease-out infinite}"
             "@keyframes pulse{0%{transform:scale(1);opacity:.9}100%{transform:scale(2.6);opacity:0}}"
             "@media (prefers-reduced-motion: reduce){.pulse{animation:none;opacity:0}}")
    return f"""<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 {w} {h}" width="{w}" height="{h}" role="img" aria-label="Roadmap: M0 Foundation and M1 Engine are done, M2 Safety is being built, then M3 to M8.">
{style(c, extra)}
<rect x=".5" y=".5" width="{w-1}" height="{h-1}" rx="18" class="bg rule" stroke-width="1"/>
{''.join(out)}
</svg>
"""


write("roadmap-dark.svg", roadmap("dark"))
write("roadmap-light.svg", roadmap("light"))


# ---------- palette card ----------
def palette():
    w, h = 1280, 330
    roles = [("bg", "Background"), ("surface", "Surface"), ("rule", "Rule"), ("text", "Text"), ("muted", "Muted"),
             ("accent", "Kesariya"), ("brass", "Brass"), ("sage", "Sage"), ("amber", "Amber"), ("sindoor", "Sindoor")]
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
