# Fonts

The artwork in `docs/assets` and the docs site use two typefaces, embedded so they look
the same on every operating system:

| File | Typeface | Used for |
|---|---|---|
| `inter-display-semibold.woff2` | Inter Display SemiBold | the wordmark, titles, headings |
| `inter-regular.woff2`, `inter-bold.woff2` | Inter | the tagline and body text (bold on the docs site) |
| `jetbrains-mono-regular.woff2`, `jetbrains-mono-bold.woff2` | JetBrains Mono | terminal text and code |

Each file is a Latin subset made with fontTools from the upstream fonts:

```sh
pyftsubset Inter-Regular.otf --flavor=woff2 --layout-features='kern,liga,calt,tnum' \
  --unicodes='U+0020-007E,U+00A0-00FF,U+2013-2014,U+2018-201D,U+2022,U+2026,U+2190-2193,U+2605,U+2713,U+2717' \
  --output-file=inter-regular.woff2
```

- Inter: Copyright 2016 The Inter Project Authors (https://github.com/rsms/inter)
- JetBrains Mono: Copyright 2020 The JetBrains Mono Project Authors (https://github.com/JetBrains/JetBrainsMono)

Both are licensed under the SIL Open Font License 1.1; see [`OFL.txt`](OFL.txt).
