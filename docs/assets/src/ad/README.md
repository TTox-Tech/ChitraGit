# ChitraGit promo

The 2-minute promo in the README and on the docs site is rendered from this folder.

| File | What it is |
|---|---|
| `ad.html` | The whole film as one HTML timeline. `seek(t)` draws time `t` in seconds, so every frame is reproducible. The voiceover lines and on-screen captions are the `SUBS` table. |
| `render.mjs` | Steps the timeline with Playwright, then builds `chitragit-ad.mp4` (1080p60), `chitragit-ad.gif` (800 px, 24 fps) and `chitragit-ad-poster.png` with ffmpeg and gifsicle. |
| `fonts/` | M PLUS Code Latin (SIL Open Font License, see `fonts/OFL.txt`). |

```sh
node docs/assets/src/ad/render.mjs /tmp/ad --sample 12,47.5,90   # a few stills, seconds
node docs/assets/src/ad/render.mjs /tmp/ad                       # full render, ~20 min
node docs/assets/src/ad/render.mjs /tmp/ad --encode-only         # re-encode existing frames
```

Colours follow [`docs/brand.md`](../../../brand.md). Screens for features that are still being
built are drawn in the timeline; swap them for real footage as those features land.
