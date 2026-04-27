package lyrics

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"

	"github.com/navidrome/navidrome/model"
	"github.com/navidrome/navidrome/utils/gg"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("sources", func() {
	ctx := context.Background()

	Describe("fromEmbedded", func() {
		It("should return nothing for a media file with no lyrics", func() {
			mf := model.MediaFile{}
			lyrics, err := fromEmbedded(ctx, &mf)

			Expect(err).To(BeNil())
			Expect(lyrics).To(HaveLen(0))
		})

		It("should return lyrics for a media file with well-formatted lyrics", func() {
			const syncedLyrics = "[00:18.80]We're no strangers to love\n[00:22.801]You know the rules and so do I"
			const unsyncedLyrics = "We're no strangers to love\nYou know the rules and so do I"

			synced, _ := model.ToLyrics("eng", syncedLyrics)
			unsynced, _ := model.ToLyrics("xxx", unsyncedLyrics)

			expectedList := model.LyricList{*synced, *unsynced}
			lyricsJson, err := json.Marshal(expectedList)

			Expect(err).ToNot(HaveOccurred())

			mf := model.MediaFile{
				Lyrics: string(lyricsJson),
			}

			lyrics, err := fromEmbedded(ctx, &mf)
			Expect(err).To(BeNil())
			Expect(lyrics).ToNot(BeNil())
			Expect(lyrics).To(Equal(expectedList))
		})

		It("should return an error if somehow the JSON is bad", func() {
			mf := model.MediaFile{Lyrics: "["}
			lyrics, err := fromEmbedded(ctx, &mf)

			Expect(lyrics).To(HaveLen(0))
			Expect(err).ToNot(BeNil())
		})
	})

	Describe("fromExternalFile", func() {
		It("should return nil for lyrics that don't exist", func() {
			mf := model.MediaFile{Path: "tests/fixtures/01 Invisible (RED) Edit Version.mp3"}
			lyrics, err := fromExternalFile(ctx, &mf, ".lrc")

			Expect(err).To(BeNil())
			Expect(lyrics).To(HaveLen(0))
		})

		It("should return synchronized lyrics from a file", func() {
			mf := model.MediaFile{Path: "tests/fixtures/test.mp3"}
			lyrics, err := fromExternalFile(ctx, &mf, ".lrc")

			Expect(err).To(BeNil())
			Expect(lyrics).To(Equal(model.LyricList{
				model.Lyrics{
					DisplayArtist: "Rick Astley",
					DisplayTitle:  "That one song",
					Format:        "lrc",
					Lang:          "eng",
					Line: []model.Line{
						{
							Start: gg.P(int64(18800)),
							Value: "We're no strangers to love",
						},
						{
							Start: gg.P(int64(22801)),
							Value: "You know the rules and so do I",
						},
					},
					Offset: gg.P(int64(-100)),
					Raw:    "[ar:Rick Astley]\n[ti:That one song]\n[offset:-100]\n[lang:eng]\n[00:18.80]We're no strangers to love\n[00:22.801]You know the rules and so do I\n",
					Synced: true,
				},
			}))
		})

		It("should return raw TTML lyrics from a file", func() {
			mf := model.MediaFile{Path: "tests/fixtures/test.mp3"}
			lyrics, err := fromExternalTTML(ctx, &mf, ".ttml")

			Expect(err).To(BeNil())
			Expect(lyrics).To(HaveLen(1))
			Expect(lyrics[0].Format).To(Equal("ttml"))
			Expect(lyrics[0].Raw).To(ContainSubstring("<tt xmlns=\"http://www.w3.org/ns/ttml\">"))
			Expect(lyrics[0].Line).To(BeEmpty())
			Expect(lyrics[0].Synced).To(BeTrue())
		})

		It("should find TTML lyrics anywhere under the artist folder", func() {
			libraryPath := GinkgoT().TempDir()
			linkinLyrics := `<tt xmlns="http://www.w3.org/ns/ttml"><body>linkin park lyrics</body></tt>`
			museLyrics := `<tt xmlns="http://www.w3.org/ns/ttml"><body>muse lyrics</body></tt>`

			Expect(os.MkdirAll(filepath.Join(libraryPath, "Linkin Park", "Meteora"), 0755)).To(Succeed())
			Expect(os.MkdirAll(filepath.Join(libraryPath, "Muse", "Origin of Symmetry"), 0755)).To(Succeed())
			Expect(os.WriteFile(filepath.Join(libraryPath, "Linkin Park", "Meteora", "Blackout.ttml"), []byte(linkinLyrics), 0644)).To(Succeed())
			Expect(os.WriteFile(filepath.Join(libraryPath, "Muse", "Origin of Symmetry", "Blackout.ttml"), []byte(museLyrics), 0644)).To(Succeed())

			mf := model.MediaFile{
				LibraryPath: libraryPath,
				Path:        filepath.Join("Linkin Park", "A Thousand Suns (Bonus Edition)", "Blackout.flac"),
				Title:       "Blackout",
			}
			lyrics, err := fromExternalTTML(ctx, &mf, ".ttml")

			Expect(err).To(BeNil())
			Expect(lyrics).To(HaveLen(1))
			Expect(lyrics[0].Raw).To(ContainSubstring("linkin park lyrics"))
			Expect(lyrics[0].Raw).ToNot(ContainSubstring("muse lyrics"))
		})

		It("should match recursive lyrics by track title when the audio filename has a track number", func() {
			libraryPath := GinkgoT().TempDir()
			Expect(os.MkdirAll(filepath.Join(libraryPath, "Linkin Park", "A Thousand Suns"), 0755)).To(Succeed())
			Expect(os.WriteFile(filepath.Join(libraryPath, "Linkin Park", "A Thousand Suns", "Blackout.ttml"), []byte(`<tt>title match</tt>`), 0644)).To(Succeed())

			mf := model.MediaFile{
				LibraryPath: libraryPath,
				Path:        filepath.Join("Linkin Park", "Meteora", "03 Blackout.flac"),
				Title:       "Blackout",
			}
			lyrics, err := fromExternalTTML(ctx, &mf, ".ttml")

			Expect(err).To(BeNil())
			Expect(lyrics).To(HaveLen(1))
			Expect(lyrics[0].Raw).To(ContainSubstring("title match"))
		})

		It("should return unsynchronized lyrics from a file", func() {
			mf := model.MediaFile{Path: "tests/fixtures/test.mp3"}
			lyrics, err := fromExternalFile(ctx, &mf, ".txt")

			Expect(err).To(BeNil())
			Expect(lyrics).To(Equal(model.LyricList{
				model.Lyrics{
					Lang: "xxx",
					Line: []model.Line{
						{
							Value: "We're no strangers to love",
						},
						{
							Value: "You know the rules and so do I",
						},
					},
					Synced: false,
				},
			}))
		})

		It("should handle LRC files with UTF-8 BOM marker (issue #4631)", func() {
			// The function looks for <basePath-without-ext><suffix>, so we need to pass
			// a MediaFile with .mp3 path and look for .lrc suffix
			mf := model.MediaFile{Path: "tests/fixtures/bom-test.mp3"}
			lyrics, err := fromExternalFile(ctx, &mf, ".lrc")

			Expect(err).To(BeNil())
			Expect(lyrics).ToNot(BeNil())
			Expect(lyrics).To(HaveLen(1))

			// The critical assertion: even with BOM, synced should be true
			Expect(lyrics[0].Synced).To(BeTrue(), "Lyrics with BOM marker should be recognized as synced")
			Expect(lyrics[0].Line).To(HaveLen(1))
			Expect(lyrics[0].Line[0].Start).To(Equal(gg.P(int64(0))))
			Expect(lyrics[0].Line[0].Value).To(ContainSubstring("作曲"))
		})

		It("should handle UTF-16 LE encoded LRC files", func() {
			mf := model.MediaFile{Path: "tests/fixtures/bom-utf16-test.mp3"}
			lyrics, err := fromExternalFile(ctx, &mf, ".lrc")

			Expect(err).To(BeNil())
			Expect(lyrics).ToNot(BeNil())
			Expect(lyrics).To(HaveLen(1))

			// UTF-16 should be properly converted to UTF-8
			Expect(lyrics[0].Synced).To(BeTrue(), "UTF-16 encoded lyrics should be recognized as synced")
			Expect(lyrics[0].Line).To(HaveLen(2))
			Expect(lyrics[0].Line[0].Start).To(Equal(gg.P(int64(18800))))
			Expect(lyrics[0].Line[0].Value).To(Equal("We're no strangers to love"))
			Expect(lyrics[0].Line[1].Start).To(Equal(gg.P(int64(22801))))
			Expect(lyrics[0].Line[1].Value).To(Equal("You know the rules and so do I"))
		})
	})
})
