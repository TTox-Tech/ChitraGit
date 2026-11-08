# Fonts

The artwork in `docs/assets` and the docs site use these fonts, embedded so they look the
same on every operating system:

| File | Typeface | Used for |
|---|---|---|
| `m-plus-code-latin.woff2` | M PLUS Code Latin, variable weight 100 to 700 | the brand font: wordmark, titles, artwork text, code |
| `inter-regular.woff2`, `inter-bold.woff2` | Inter | body text on the docs site, and the `✓ ✗ ★` symbols in the artwork |

Each file is a Latin subset made with fontTools from the upstream fonts. M PLUS Code
Latin keeps its weight axis and has its width axis pinned at 100:

```sh
fonttools varLib.instancer 'MPLUSCodeLatin[wdth,wght].ttf' wdth=100 -o mplus.ttf
pyftsubset mplus.ttf --flavor=woff2 --layout-features='kern,liga,calt,tnum' \
  --unicodes='U+0020-007E,U+00A0-00FF,U+2013-2014,U+2018-201D,U+2022,U+2026,U+2500-257F' \
  --output-file=m-plus-code-latin.woff2
```

- M PLUS Code Latin: Copyright 2021 The M+ FONTS Project Authors (https://github.com/coz-m/MPLUS_FONTS)
- Inter: Copyright 2016 The Inter Project Authors (https://github.com/rsms/inter)

Both are licensed under the SIL Open Font License 1.1; see [`OFL.txt`](OFL.txt).
