package lyrics

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"

	"github.com/navidrome/navidrome/log"
	"github.com/navidrome/navidrome/model"
	"github.com/navidrome/navidrome/utils/ioutils"
)

func fromEmbedded(ctx context.Context, mf *model.MediaFile) (model.LyricList, error) {
	if mf.Lyrics != "" {
		log.Trace(ctx, "embedded lyrics found in file", "title", mf.Title)
		return mf.StructuredLyrics()
	}

	log.Trace(ctx, "no embedded lyrics for file", "path", mf.Title)

	return nil, nil
}

func fromExternalFile(ctx context.Context, mf *model.MediaFile, suffix string) (model.LyricList, error) {
	externalLyric, err := findExternalLyric(ctx, mf, suffix)
	if err != nil {
		return nil, err
	} else if externalLyric == "" {
		return nil, nil
	}

	contents, err := ioutils.UTF8ReadFile(externalLyric)
	if err != nil {
		return nil, err
	}

	lyrics, err := model.ToLyrics("xxx", string(contents))
	if err != nil {
		log.Error(ctx, "error parsing lyric external file", "path", externalLyric, err)
		return nil, err
	} else if lyrics == nil {
		log.Trace(ctx, "empty lyrics from external file", "path", externalLyric)
		return nil, nil
	}

	if strings.EqualFold(suffix, ".lrc") {
		lyrics.Format = "lrc"
		lyrics.Raw = string(contents)
	}
	log.Trace(ctx, "retrieved lyrics from external file", "path", externalLyric)

	return model.LyricList{*lyrics}, nil
}

func fromExternalTTML(ctx context.Context, mf *model.MediaFile, suffix string) (model.LyricList, error) {
	externalLyric, err := findExternalLyric(ctx, mf, suffix)
	if err != nil {
		return nil, err
	} else if externalLyric == "" {
		return nil, nil
	}

	contents, err := ioutils.UTF8ReadFile(externalLyric)
	if err != nil {
		return nil, err
	}

	raw := strings.TrimSpace(string(contents))
	if raw == "" {
		log.Trace(ctx, "empty lyrics from external file", "path", externalLyric)
		return nil, nil
	}

	log.Trace(ctx, "retrieved TTML lyrics from external file", "path", externalLyric)
	return model.LyricList{{
		Format: "ttml",
		Lang:   "xxx",
		Raw:    raw,
		Synced: true,
	}}, nil
}

func findExternalLyric(ctx context.Context, mf *model.MediaFile, suffix string) (string, error) {
	basePath := mf.AbsolutePath()
	externalLyric := strings.TrimSuffix(basePath, filepath.Ext(basePath)) + suffix

	if _, err := os.Stat(externalLyric); err == nil {
		return externalLyric, nil
	} else if !errors.Is(err, os.ErrNotExist) {
		return "", err
	}

	artistLyric, err := findExternalLyricInArtistFolder(mf, suffix)
	if err != nil {
		return "", err
	} else if artistLyric != "" {
		return artistLyric, nil
	}

	log.Trace(ctx, "no lyrics found at path", "path", externalLyric)
	return "", nil
}

func findExternalLyricInArtistFolder(mf *model.MediaFile, suffix string) (string, error) {
	artistFolder := artistFolderPath(mf)
	if artistFolder == "" {
		return "", nil
	}

	audioStem := strings.TrimSuffix(filepath.Base(mf.Path), filepath.Ext(mf.Path))
	title := strings.TrimSpace(mf.Title)
	if audioStem == "" && title == "" {
		return "", nil
	}

	var found string
	err := filepath.WalkDir(artistFolder, func(candidate string, entry os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() {
			return nil
		}
		if !strings.EqualFold(filepath.Ext(candidate), suffix) {
			return nil
		}

		stem := strings.TrimSuffix(filepath.Base(candidate), filepath.Ext(candidate))
		if (audioStem != "" && strings.EqualFold(stem, audioStem)) || (title != "" && strings.EqualFold(stem, title)) {
			found = candidate
			return filepath.SkipAll
		}
		return nil
	})
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return "", err
	}
	return found, nil
}

func artistFolderPath(mf *model.MediaFile) string {
	cleanPath := filepath.Clean(filepath.FromSlash(mf.Path))
	if cleanPath == "." || cleanPath == string(filepath.Separator) {
		return ""
	}

	parts := strings.Split(cleanPath, string(filepath.Separator))
	if len(parts) < 2 || parts[0] == "." || parts[0] == ".." || parts[0] == "" {
		return ""
	}
	return filepath.Join(mf.LibraryPath, parts[0])
}

// fromPlugin attempts to load lyrics from a plugin with the given name.
func (l *lyricsService) fromPlugin(ctx context.Context, mf *model.MediaFile, pluginName string) (model.LyricList, error) {
	if l.pluginLoader == nil {
		log.Debug(ctx, "Invalid lyric source", "source", pluginName)
		return nil, nil
	}

	provider, ok := l.pluginLoader.LoadLyricsProvider(pluginName)
	if !ok {
		log.Warn(ctx, "Lyrics plugin not found", "plugin", pluginName)
		return nil, nil
	}

	lyricsList, err := provider.GetLyrics(ctx, mf)
	if err != nil {
		return nil, err
	}

	if len(lyricsList) > 0 {
		log.Trace(ctx, "Retrieved lyrics from plugin", "plugin", pluginName, "count", len(lyricsList))
	}
	return lyricsList, nil
}
