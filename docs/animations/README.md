# Animations

[scanner-goroutines.mp4](scanner-goroutines.mp4) is a toy animation of the goroutines in the bucket scanner: one reader per file behind a semaphore, pooled chunk buffers, encode workers scattering codes into bucket files, the barrier between the phases, and count workers taking one bucket at a time. It uses 3 files, 4 buckets and a handful of codes; the real build sizes everything from its memory budget. For the full story, please refer to [ARCHITECTURE.md](../../ARCHITECTURE.md#how-the-valid-set-is-built).

## Re-rendering

The script uses [Manim Community](https://www.manim.community/) and needs ffmpeg on the `PATH`; LaTeX is not needed. From this folder:

```sh
python -m venv .venv
.venv/Scripts/pip install manim          # .venv/bin/pip on Linux and macOS
.venv/Scripts/manim -qm scanner_goroutines.py ScannerGoroutines
```

The video is written under `media/videos/scanner_goroutines/720p30/`. Please copy it over `scanner-goroutines.mp4` if you'd like to update the committed one. Use `-ql` for a quick low-quality draft.
