# Portable report renderer QA

The canonical delivery command was run twice after targeted artifact corrections. Its browser verifier reported an 8 px desktop overflow caused by the packaged reader's full-viewport top bar when Chromium reserved 15 px for a vertical scrollbar:

- viewport width: 1440 px
- document client width: 1425 px
- top-bar width: 1440 px
- top-bar bounds: -7.5 px to 1432.5 px

The final file still uses the canonical artifact builder and shared chart SVG extractor. A single containment rule was then added to the generated shell: `html,body{overflow-x:hidden}`. No report data, blocks, source metadata, chart encodings, or reader runtime were changed.

The canonical verifier then passed:

- exact artifact structure: 16 blocks, 4 metrics, 1 chart, 1 table
- source interaction: keyboard menu and semantic click passed
- desktop viewport: 1440 px
- narrow viewport: 390 px
- external network calls: none
- browser or console errors: none
