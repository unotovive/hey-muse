//go:build !dot

package display

import (
	_ "embed"
	"errors"
	"image"
	"log/slog"
	"sync"

	"golang.org/x/image/font"
	"golang.org/x/image/font/opentype"
	"golang.org/x/image/math/fixed"

	"github.com/HuskerMinion/techo5/echod/internal/lib/fonts"
)

// Noto Sans CJK JP is distributed under the SIL Open Font License in fonts/LICENSE.
// Keep Go's existing Latin faces and metrics; use Noto only for missing glyphs.
//
//go:embed fonts/NotoSansCJKjp-Regular.otf
var japaneseFontData []byte

var japaneseFontOnce sync.Once
var japaneseFont *opentype.Font

func newTextFace(primary *opentype.Font, options *opentype.FaceOptions) (font.Face, error) {
	face, err := opentype.NewFace(primary, options)
	if err != nil {
		return nil, err
	}
	japaneseFontOnce.Do(parseJapaneseFont)
	if japaneseFont == nil {
		return face, nil
	}
	fallback, err := opentype.NewFace(japaneseFont, options)
	if err != nil {
		face.Close()
		return nil, err
	}
	return &fallbackTextFace{Face: face, fallback: fallback}, nil
}

// The embedded font is lib/fonts' last resort as well, under the screen font, Go and the owner's own
// fonts, so every face the screen makes (fontFace) draws Japanese without a download; a downloaded
// Japanese font (feature/cjkfont) still draws what it has first.
func init() {
	japaneseFontOnce.Do(parseJapaneseFont)
	fonts.SetLastResort(japaneseFont)
}

func parseJapaneseFont() {
	var err error
	japaneseFont, err = opentype.Parse(japaneseFontData)
	if err != nil {
		slog.Error("parsing Japanese fallback font failed", "err", err)
	}
}

type fallbackTextFace struct {
	font.Face
	fallback font.Face
}

func (f *fallbackTextFace) forRune(r rune) font.Face {
	if _, ok := f.Face.GlyphAdvance(r); ok {
		return f.Face
	}
	if _, ok := f.fallback.GlyphAdvance(r); ok {
		return f.fallback
	}
	return f.Face
}

func (f *fallbackTextFace) Glyph(dot fixed.Point26_6, r rune) (image.Rectangle, image.Image, image.Point, fixed.Int26_6, bool) {
	return f.forRune(r).Glyph(dot, r)
}

func (f *fallbackTextFace) GlyphBounds(r rune) (fixed.Rectangle26_6, fixed.Int26_6, bool) {
	return f.forRune(r).GlyphBounds(r)
}

func (f *fallbackTextFace) GlyphAdvance(r rune) (fixed.Int26_6, bool) {
	return f.forRune(r).GlyphAdvance(r)
}

func (f *fallbackTextFace) Kern(r0, r1 rune) fixed.Int26_6 {
	a, b := f.forRune(r0), f.forRune(r1)
	if a != b {
		return 0
	}
	return a.Kern(r0, r1)
}

func (f *fallbackTextFace) Close() error {
	return errors.Join(f.Face.Close(), f.fallback.Close())
}
