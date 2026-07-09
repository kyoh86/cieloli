package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestCleanTitleAndEpisode(t *testing.T) {
	title := "【#ストグラseason2】ねずみさんと新車いっぱい #99"
	if got := cleanTitle(title); got != "ねずみさんと新車いっぱい" {
		t.Fatalf("cleanTitle = %q", got)
	}
	if got := episode(title); got != 99 {
		t.Fatalf("episode = %d", got)
	}
}

func TestYouTubeKeepsTwitchDate(t *testing.T) {
	entries := map[string]Entry{}
	upsertTwitch(entries, Video{
		ID:    "2793230993",
		Title: "【#ストグラseason2】ねずみさんと新車いっぱい #99",
		// zero time is fine for this merge test; set date directly below.
	})
	for k, e := range entries {
		e.Date = "2026/06/10"
		entries[k] = e
	}
	upsertYouTube(entries, Video{
		ID:    "youtube-id",
		Title: "【#ストグラseason2】ねずみさんと新車いっぱい #99",
	})
	e := entries["ep:099"]
	if e.Date != "2026/06/10" {
		t.Fatalf("date = %q", e.Date)
	}
	if e.Source != "youtube" || e.YouTubeID != "youtube-id" || e.TwitchID != "2793230993" {
		t.Fatalf("entry = %#v", e)
	}
}

func TestRenderAtWiki(t *testing.T) {
	out := renderAtWiki([]Entry{{
		Key:       "ep:001",
		Episode:   1,
		Date:      "2026/06/10",
		Title:     "ねずみさんと新車いっぱい",
		YouTubeID: "youtube-id",
	}}, "未確認")
	if !strings.Contains(out, "#region(6月,open)&bold(){6月}") {
		t.Fatalf("missing month region:\n%s", out)
	}
	if !strings.Contains(out, "https://www.youtube.com/watch?v=youtube-id") {
		t.Fatalf("missing youtube link:\n%s", out)
	}
}

func TestParseAtWikiSeed(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "seed.atwiki")
	err := os.WriteFile(path, []byte("|2026/06/10|&color(#ff0000){&icon_fa(fa-brands fa-youtube fa-fw)}&nbsp;[[ねずみさんと新車いっぱい>>https://www.youtube.com/watch?v=youtube-id]]|\n"), 0o644)
	if err != nil {
		t.Fatal(err)
	}
	entries, err := parseAtWikiSeed(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 {
		t.Fatalf("len = %d", len(entries))
	}
	if entries[0].Date != "2026/06/10" || entries[0].YouTubeID != "youtube-id" {
		t.Fatalf("entry = %#v", entries[0])
	}
}
