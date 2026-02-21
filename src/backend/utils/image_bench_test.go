package utils

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"
)

// ── helpers ───────────────────────────────────────────────────────────────────

// benchDirs creates a self-contained temp directory tree and returns a
// Directories pointing into it.  The caller should defer os.RemoveAll(root).
func benchDirs(b *testing.B) (Directories, string) {
	b.Helper()
	root := b.TempDir()
	dirs := Directories{
		Image:     filepath.Join(root, "images"),
		Thumbnail: filepath.Join(root, "thumbnails"),
		Video:     filepath.Join(root, "videos"),
		Tmp:       filepath.Join(root, "tmp"),
		Upload:    filepath.Join(root, "upload"),
	}
	for _, d := range []string{dirs.Image, dirs.Thumbnail, dirs.Video,
		dirs.Tmp, filepath.Join(dirs.Tmp, "thumbnails"), dirs.Upload} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			b.Fatal(err)
		}
	}
	return dirs, root
}

// testInputs is the set of synthetic JPEG source files used in every benchmark.
// Odd heights stress the even-dimension crop in ConvertImageToAvif.
var testInputs = []struct {
	name string
	path string
}{
	{"640x427", "/tmp/bench_640x427.jpg"},
	{"1280x853", "/tmp/bench_1280x853.jpg"},
	{"1920x1279", "/tmp/bench_1920x1279.jpg"},
}

func skipIfMissing(b *testing.B, path string) {
	b.Helper()
	if _, err := os.Stat(path); err != nil {
		b.Skipf("test input %s missing — run utils_gen_bench_inputs.sh first", path)
	}
}

// ── ConvertImageToAvif ────────────────────────────────────────────────────────

func BenchmarkConvertImageToAvif_FullRes(b *testing.B) {
	for _, tc := range testInputs {
		tc := tc
		b.Run(tc.name, func(b *testing.B) {
			skipIfMissing(b, tc.path)
			dirs, _ := benchDirs(b)
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				dst := filepath.Join(dirs.Image, fmt.Sprintf("out_%d.avif", i))
				if err := ConvertImageToAvif(tc.path, dst, 18, 4); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}

func BenchmarkConvertImageToAvif_Thumbnail(b *testing.B) {
	for _, tc := range testInputs {
		tc := tc
		b.Run(tc.name, func(b *testing.B) {
			skipIfMissing(b, tc.path)
			dirs, _ := benchDirs(b)
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				dst := filepath.Join(dirs.Thumbnail, fmt.Sprintf("out_%d.avif", i))
				if err := ConvertImageToAvif(tc.path, dst, 30, 6); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}

// ── NormalizeImageToJPEG ──────────────────────────────────────────────────────

func BenchmarkNormalizeImageToJPEG(b *testing.B) {
	for _, tc := range testInputs {
		tc := tc
		b.Run(tc.name, func(b *testing.B) {
			skipIfMissing(b, tc.path)
			dirs, _ := benchDirs(b)
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				dst, err := NormalizeImageToJPEG(tc.path, filepath.Join(dirs.Tmp, "thumbnails"))
				if err != nil {
					b.Fatal(err)
				}
				_ = os.Remove(dst)
			}
		})
	}
}

// ── CreateThumbnailFromImage ──────────────────────────────────────────────────

func BenchmarkCreateThumbnailFromImage(b *testing.B) {
	for _, tc := range testInputs {
		tc := tc
		b.Run(tc.name, func(b *testing.B) {
			skipIfMissing(b, tc.path)
			dirs, _ := benchDirs(b)

			// Pre-load image once; benchmark only the thumbnail pipeline.
			img, err := LoadImageFile(tc.path)
			if err != nil {
				b.Fatal(err)
			}

			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				dst := filepath.Join(dirs.Thumbnail, fmt.Sprintf("thumb_%d.avif", i))
				if err := CreateThumbnailFromImage(img, dst, dirs); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}

// ── ProcessImage (end-to-end) ─────────────────────────────────────────────────

func BenchmarkProcessImage(b *testing.B) {
	for _, tc := range testInputs {
		tc := tc
		b.Run(tc.name, func(b *testing.B) {
			skipIfMissing(b, tc.path)
			dirs, _ := benchDirs(b)
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				// Copy input so ProcessImage can reference a unique filename each run.
				src := filepath.Join(dirs.Upload, fmt.Sprintf("input_%d.jpg", i))
				data, _ := os.ReadFile(tc.path)
				_ = os.WriteFile(src, data, 0o644)

				if _, err := ProcessImage(src, dirs); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}

// ── Preset comparison (CRF 18, various presets) ───────────────────────────────

func BenchmarkConvertImageToAvif_PresetComparison(b *testing.B) {
	tc := testInputs[1] // 1280x853 — medium size representative
	skipIfMissing(b, tc.path)

	for _, preset := range []int{4, 6, 8, 10} {
		preset := preset
		b.Run(fmt.Sprintf("preset%d", preset), func(b *testing.B) {
			dirs, _ := benchDirs(b)
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				dst := filepath.Join(dirs.Image, fmt.Sprintf("out_%d.avif", i))
				if err := ConvertImageToAvif(tc.path, dst, 18, preset); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}
