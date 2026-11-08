// Renders ad.html frame by frame, then builds the GIF, MP4 and poster with ffmpeg.
//
//   node docs/assets/src/ad/render.mjs [outDir] [--fps 60] [--sample 1,5.5,12] [--encode-only]
//
// --sample writes only those times as PNGs (for quick checks).
// --encode-only skips the browser and re-encodes frames already in outDir/frames.
// Needs Playwright (Chromium), ffmpeg and, for a smaller GIF, gifsicle.
import { createRequire } from "node:module";
import { execFileSync } from "node:child_process";
import { mkdirSync, rmSync, statSync } from "node:fs";
import { dirname, join, resolve } from "node:path";
import { fileURLToPath, pathToFileURL } from "node:url";

const { chromium } = createRequire(import.meta.url)("playwright");
const here = dirname(fileURLToPath(import.meta.url));
const args = process.argv.slice(2);
const opt = (name, def) => {
  const i = args.indexOf(name);
  return i >= 0 ? args.splice(i, 2)[1] : def;
};
const fps = Number(opt("--fps", "60"));
const encodeOnly = args.includes("--encode-only");
if (encodeOnly) args.splice(args.indexOf("--encode-only"), 1);
const sample = opt("--sample", "");
const out = resolve(args[0] || join(here, "out"));
const frames = join(out, "frames");
mkdirSync(out, { recursive: true });

let duration = 120;
if (!encodeOnly) {
  const browser = await chromium.launch();
  const page = await browser.newPage({ viewport: { width: 1920, height: 1080 }, deviceScaleFactor: 1 });
  await page.goto(pathToFileURL(join(here, "ad.html")).href);
  await page.evaluate(() => window.ready);
  duration = await page.evaluate(() => window.DURATION);
  const stage = await page.$("#stage");

  async function shot(t, file) {
    await page.evaluate((t) => window.seek(t), t);
    await stage.screenshot({ path: file, type: "png" });
}

if (sample) {
  for (const t of sample.split(",").map(Number)) {
    await shot(t, join(out, `t${t.toFixed(2)}.png`));
  }
  await browser.close();
  process.exit(0);
}

rmSync(frames, { recursive: true, force: true });
mkdirSync(frames, { recursive: true });
const n = Math.round(duration * fps);
for (let i = 0; i < n; i++) {
  await shot(i / fps, join(frames, `f${String(i).padStart(5, "0")}.png`));
  if (i % 150 === 0) console.log(`frame ${i}/${n}`);
}
await browser.close();
}

const ff = (...a) => execFileSync("ffmpeg", ["-loglevel", "error", "-y", ...a], { stdio: "inherit" });
const input = ["-framerate", String(fps), "-i", join(frames, "f%05d.png")];

// MP4 preview, 1080p.
ff(...input, "-c:v", "libx264", "-pix_fmt", "yuv420p", "-crf", "26", "-preset", "slow", "-movflags", "+faststart", join(out, "chitragit-ad.mp4"));

// README GIF, 800x450 at 24 fps: one palette for the whole film, then only changed rectangles per frame.
const scale = "fps=24,scale=800:-1:flags=lanczos";
// Flat colours only, so no dither: dithering turns the ink background into noise and doubles the size.
ff(...input, "-vf", `${scale},palettegen=max_colors=96:stats_mode=diff`, join(out, "palette.png"));
ff(...input, "-i", join(out, "palette.png"), "-lavfi", `${scale}[x];[x][1:v]paletteuse=dither=none:diff_mode=rectangle`, "-loop", "0", join(out, "raw.gif"));
// gifsicle's lossy pass cuts the size by about a third with no visible change at this setting.
try {
  execFileSync("gifsicle", ["-O3", "--lossy=30", join(out, "raw.gif"), "-o", join(out, "chitragit-ad.gif")], { stdio: "inherit" });
} catch {
  console.warn("gifsicle not found: keeping the unoptimised GIF");
  execFileSync("cp", [join(out, "raw.gif"), join(out, "chitragit-ad.gif")]);
}

// Poster (end card) for reduced-motion users.
ff("-i", join(frames, `f${String(Math.round((duration - 3) * fps)).padStart(5, "0")}.png`), "-vf", "scale=1280:-1:flags=lanczos", join(out, "chitragit-ad-poster.png"));

for (const f of ["chitragit-ad.gif", "chitragit-ad.mp4", "chitragit-ad-poster.png"]) {
  console.log(f, (statSync(join(out, f)).size / 1e6).toFixed(2), "MB");
}
