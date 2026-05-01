package nativeapi

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"

	"github.com/navidrome/navidrome/conf"
	"github.com/navidrome/navidrome/conf/configtest"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("About API", func() {
	BeforeEach(func() {
		DeferCleanup(configtest.SetupConfig())
	})

	It("serves markdown from the configured file", func() {
		aboutPath := filepath.Join(GinkgoT().TempDir(), "about.md")
		Expect(os.WriteFile(aboutPath, []byte("# About Waves Music\nWaves music is a project made by Ori Hauptman.\n"), 0600)).To(Succeed())
		conf.Server.WavesMusicAboutPath = aboutPath

		req := httptest.NewRequest(http.MethodGet, "/api/about", nil)
		w := httptest.NewRecorder()

		getAbout(w, req)

		Expect(w.Code).To(Equal(http.StatusOK))
		var resp aboutResponse
		Expect(json.Unmarshal(w.Body.Bytes(), &resp)).To(Succeed())
		Expect(resp.ID).To(Equal("about"))
		Expect(resp.Format).To(Equal("markdown"))
		Expect(resp.Markdown).To(ContainSubstring("# About Waves Music"))
	})

	It("returns not found when no markdown path is configured", func() {
		conf.Server.WavesMusicAboutPath = ""

		req := httptest.NewRequest(http.MethodGet, "/api/about", nil)
		w := httptest.NewRecorder()

		getAbout(w, req)

		Expect(w.Code).To(Equal(http.StatusNotFound))
	})
})
