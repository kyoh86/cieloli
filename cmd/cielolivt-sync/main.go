package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"html"
	"io"
	"net/http"
	"net/url"
	"os"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"
)

const twitchClientID = "kimne78kx3ncx6brgo4mv6wki5h1ko"

type Entry struct {
	Key        string `json:"key"`
	Episode    int    `json:"episode,omitempty"`
	Date       string `json:"date"`
	Title      string `json:"title"`
	TwitchID   string `json:"twitch_id,omitempty"`
	YouTubeID  string `json:"youtube_id,omitempty"`
	Source     string `json:"source"`
	LastSeenAt string `json:"last_seen_at"`
}

type State struct {
	Entries []Entry `json:"entries"`
}

type Video struct {
	ID        string
	Title     string
	CreatedAt time.Time
	Source    string
}

func main() {
	var (
		channel      = flag.String("twitch-channel", "cielolivt", "Twitch channel login")
		youtubeURL   = flag.String("youtube-url", "https://www.youtube.com/@cieloliVT/videos", "YouTube channel videos URL")
		statePath    = flag.String("state", "cielolivt-streams.json", "state JSON path")
		seedPath     = flag.String("seed-atwiki", "", "existing atwiki table path to seed dates and links")
		outPath      = flag.String("out", "", "atwiki output path; stdout when empty")
		include      = flag.String("include", "ねずみさん", "title substring filter")
		season       = flag.String("season", "season2", "optional season substring filter; empty disables")
		fetchYT      = flag.Bool("youtube", true, "fetch YouTube archives")
		fetchTW      = flag.Bool("twitch", true, "fetch Twitch VODs")
		maxYTPages   = flag.Int("youtube-pages", 80, "maximum YouTube continuation pages")
		twitchLimit  = flag.Int("twitch-limit", 50, "Twitch VOD fetch limit")
		unknownMonth = flag.String("unknown-month", "未確認", "region name for yyyy/mm/dd rows")
	)
	flag.Parse()

	ctx := context.Background()
	st, err := loadState(*statePath)
	if err != nil {
		exitErr(err)
	}
	entries := stateIndex(st)

	if *seedPath != "" {
		seed, err := parseAtWikiSeed(*seedPath)
		if err != nil {
			exitErr(fmt.Errorf("parse seed atwiki: %w", err))
		}
		for _, e := range seed {
			mergeSeed(entries, e)
		}
	}

	if *fetchTW {
		videos, err := fetchTwitch(ctx, *channel, *twitchLimit)
		if err != nil {
			exitErr(fmt.Errorf("fetch twitch: %w", err))
		}
		for _, v := range videos {
			if !wanted(v.Title, *include, *season) {
				continue
			}
			upsertTwitch(entries, v)
		}
	}

	if *fetchYT {
		videos, err := fetchYouTube(ctx, *youtubeURL, *maxYTPages)
		if err != nil {
			exitErr(fmt.Errorf("fetch youtube: %w", err))
		}
		for _, v := range videos {
			if !wanted(v.Title, *include, *season) {
				continue
			}
			upsertYouTube(entries, v)
		}
	}

	next := State{Entries: sortedEntries(entries)}
	if err := saveState(*statePath, next); err != nil {
		exitErr(err)
	}

	rendered := renderAtWiki(next.Entries, *unknownMonth)
	if *outPath == "" {
		fmt.Print(rendered)
		return
	}
	if err := os.WriteFile(*outPath, []byte(rendered), 0o644); err != nil {
		exitErr(err)
	}
}

func exitErr(err error) {
	fmt.Fprintln(os.Stderr, err)
	os.Exit(1)
}

func loadState(path string) (State, error) {
	b, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return State{}, nil
	}
	if err != nil {
		return State{}, err
	}
	if len(bytes.TrimSpace(b)) == 0 {
		return State{}, nil
	}
	var st State
	if err := json.Unmarshal(b, &st); err != nil {
		return State{}, err
	}
	return st, nil
}

func saveState(path string, st State) error {
	b, err := json.MarshalIndent(st, "", "  ")
	if err != nil {
		return err
	}
	b = append(b, '\n')
	return os.WriteFile(path, b, 0o644)
}

func stateIndex(st State) map[string]Entry {
	out := make(map[string]Entry, len(st.Entries))
	for _, e := range st.Entries {
		if e.Key == "" {
			e.Key = keyFor(e.Title, e.Episode)
		} else if key := keyFor(e.Title, e.Episode); strings.HasPrefix(e.Key, "ep:") && key != e.Key {
			e.Key = key
		}
		out[e.Key] = e
	}
	return out
}

func upsertTwitch(entries map[string]Entry, v Video) {
	title := cleanTitle(v.Title)
	ep := episode(v.Title)
	key := keyFor(title, ep)
	e, ok := entries[key]
	if !ok && ep != 0 {
		if candidate, candidateKey, exists := matchByEpisode(entries, title, ep); exists {
			e = candidate
			key = candidateKey
		}
	}
	if e.Key == "" {
		e.Key = key
		e.Title = title
		e.Episode = ep
	}
	if e.Date == "" || e.Date == "yyyy/mm/dd" {
		e.Date = v.CreatedAt.In(jst()).Format("2006/01/02")
	}
	if e.Title == "" {
		e.Title = title
	}
	if e.Episode == 0 {
		e.Episode = ep
	}
	e.TwitchID = v.ID
	if e.YouTubeID == "" {
		e.Source = "twitch"
	}
	e.LastSeenAt = time.Now().Format(time.RFC3339)
	nextKey := keyFor(e.Title, e.Episode)
	if nextKey != key {
		delete(entries, key)
		key = nextKey
	}
	e.Key = key
	entries[key] = e
}

func upsertYouTube(entries map[string]Entry, v Video) {
	title := cleanTitle(v.Title)
	ep := episode(v.Title)
	key := keyFor(title, ep)
	e, ok := entries[key]
	if !ok && ep != 0 {
		if candidate, candidateKey, exists := matchByEpisode(entries, title, ep); exists {
			e = candidate
			key = candidateKey
			ok = true
		}
	}
	if !ok && ep != 0 {
		titleKey := keyFor(title, 0)
		if candidate, exists := entries[titleKey]; exists {
			e = candidate
			key = titleKey
			ok = true
		}
	}
	if !ok {
		e = Entry{Key: key, Date: "yyyy/mm/dd", Title: title, Episode: ep}
	}
	if e.Title == "" {
		e.Title = title
	}
	if e.Episode == 0 {
		e.Episode = ep
	}
	e.YouTubeID = v.ID
	e.Source = "youtube"
	e.LastSeenAt = time.Now().Format(time.RFC3339)
	nextKey := keyFor(e.Title, e.Episode)
	if nextKey != key {
		delete(entries, key)
		key = nextKey
	}
	e.Key = key
	entries[key] = e
}

func mergeSeed(entries map[string]Entry, seed Entry) {
	key := seed.Key
	if key == "" {
		key = keyFor(seed.Title, seed.Episode)
	}
	e, ok := entries[key]
	if !ok && seed.Episode != 0 {
		if candidate, candidateKey, exists := matchByEpisode(entries, seed.Title, seed.Episode); exists {
			e = candidate
			key = candidateKey
			ok = true
		}
	}
	if !ok {
		e = Entry{Key: key}
	}
	if e.Date == "" || e.Date == "yyyy/mm/dd" {
		e.Date = seed.Date
	}
	if e.Title == "" {
		e.Title = seed.Title
	}
	if e.Episode == 0 {
		e.Episode = seed.Episode
	}
	if e.TwitchID == "" {
		e.TwitchID = seed.TwitchID
	}
	if e.YouTubeID == "" {
		e.YouTubeID = seed.YouTubeID
	}
	if e.Source == "" {
		e.Source = seed.Source
	}
	nextKey := keyFor(e.Title, e.Episode)
	if nextKey != key {
		delete(entries, key)
		key = nextKey
	}
	e.Key = key
	entries[key] = e
}

func matchByEpisode(entries map[string]Entry, title string, ep int) (Entry, string, bool) {
	part := titlePart(title)
	var fallback Entry
	fallbackKey := ""
	fallbackCount := 0
	for key, candidate := range entries {
		if candidate.Episode != ep {
			continue
		}
		if part != "" && titlePart(candidate.Title) == part {
			return candidate, key, true
		}
		if normalizeTitle(candidate.Title) == normalizeTitle(title) {
			return candidate, key, true
		}
		fallback = candidate
		fallbackKey = key
		fallbackCount++
	}
	if part == "" && fallbackCount == 1 {
		return fallback, fallbackKey, true
	}
	return Entry{}, "", false
}

func sortedEntries(entries map[string]Entry) []Entry {
	out := make([]Entry, 0, len(entries))
	for _, e := range entries {
		out = append(out, e)
	}
	sort.Slice(out, func(i, j int) bool {
		di, dj := out[i].Date, out[j].Date
		if di == "" {
			di = "yyyy/mm/dd"
		}
		if dj == "" {
			dj = "yyyy/mm/dd"
		}
		if di == "yyyy/mm/dd" && dj != "yyyy/mm/dd" {
			return true
		}
		if dj == "yyyy/mm/dd" && di != "yyyy/mm/dd" {
			return false
		}
		if di != dj {
			return di < dj
		}
		if out[i].Episode != out[j].Episode {
			return out[i].Episode < out[j].Episode
		}
		return out[i].Title < out[j].Title
	})
	return out
}

func renderAtWiki(entries []Entry, unknownMonth string) string {
	var b strings.Builder
	current := ""
	for _, e := range entries {
		month := unknownMonth
		if isDate(e.Date) {
			parts := strings.Split(e.Date, "/")
			month = strconv.Itoa(mustAtoi(parts[1])) + "月"
		}
		if month != current {
			if current != "" {
				b.WriteString("#table_sorter(){head=#e6e6fa,even=white,repeathead=10}\n")
				b.WriteString("#endregion\n")
			}
			current = month
			b.WriteString(fmt.Sprintf("#region(%s,open)&bold(){%s}\n", month, month))
			b.WriteString("#table_style(head=#d6ffd6){No=#eee:center,リンク=#fff}\n")
			b.WriteString("|~配信日|~リンク|\n")
		}
		b.WriteString(renderRow(e))
		b.WriteByte('\n')
	}
	if current != "" {
		b.WriteString("#table_sorter(){head=#e6e6fa,even=white,repeathead=10}\n")
		b.WriteString("#endregion\n")
	}
	return b.String()
}

func renderRow(e Entry) string {
	date := e.Date
	if date == "" {
		date = "yyyy/mm/dd"
	}
	title := atwikiText(e.Title)
	if e.YouTubeID != "" {
		return fmt.Sprintf("|%s|&color(#ff0000){&icon_fa(fa-brands fa-youtube fa-fw)}&nbsp;[[%s>>https://www.youtube.com/watch?v=%s]]|", date, title, e.YouTubeID)
	}
	if e.TwitchID != "" {
		return fmt.Sprintf("|%s|&color(#9147ff){&icon_fa(fa-brands fa-twitch fa-fw)}&nbsp;[[%s>>https://www.twitch.tv/videos/%s]]|", date, title, e.TwitchID)
	}
	return fmt.Sprintf("|%s|%s|", date, title)
}

func fetchTwitch(ctx context.Context, channel string, limit int) ([]Video, error) {
	query := `query($login:String!,$first:Int!){user(login:$login){videos(first:$first,type:ARCHIVE,sort:TIME){edges{node{id title createdAt}}}}}`
	payload := map[string]any{
		"query":     query,
		"variables": map[string]any{"login": channel, "first": limit},
	}
	var resp struct {
		Data struct {
			User *struct {
				Videos struct {
					Edges []struct {
						Node struct {
							ID        string `json:"id"`
							Title     string `json:"title"`
							CreatedAt string `json:"createdAt"`
						} `json:"node"`
					} `json:"edges"`
				} `json:"videos"`
			} `json:"user"`
		} `json:"data"`
		Errors []struct {
			Message string `json:"message"`
		} `json:"errors"`
	}
	if err := postJSON(ctx, "https://gql.twitch.tv/gql", payload, &resp, map[string]string{
		"Client-ID": twitchClientID,
	}); err != nil {
		return nil, err
	}
	if len(resp.Errors) > 0 {
		return nil, errors.New(resp.Errors[0].Message)
	}
	if resp.Data.User == nil {
		return nil, fmt.Errorf("twitch user not found: %s", channel)
	}
	out := make([]Video, 0, len(resp.Data.User.Videos.Edges))
	for _, edge := range resp.Data.User.Videos.Edges {
		t, err := time.Parse(time.RFC3339, edge.Node.CreatedAt)
		if err != nil {
			return nil, err
		}
		out = append(out, Video{ID: edge.Node.ID, Title: edge.Node.Title, CreatedAt: t, Source: "twitch"})
	}
	return out, nil
}

func fetchYouTube(ctx context.Context, videosURL string, maxPages int) ([]Video, error) {
	body, err := get(ctx, videosURL, nil)
	if err != nil {
		return nil, err
	}
	initial, err := extractJSON(body, `var ytInitialData = `)
	if err != nil {
		return nil, err
	}
	var data any
	if err := json.Unmarshal([]byte(initial), &data); err != nil {
		return nil, err
	}
	apiKey := firstMatch(body, `"INNERTUBE_API_KEY":"([^"]+)`)
	clientVersion := firstMatch(body, `"INNERTUBE_CLIENT_VERSION":"([^"]+)`)
	if clientVersion == "" {
		clientVersion = "2.20260708.01.00"
	}
	out := extractYouTubeVideos(data)
	tokens := continuationTokens(data)
	seenTokens := map[string]bool{}
	for pages := 0; len(tokens) > 0 && pages < maxPages; {
		token := tokens[0]
		tokens = tokens[1:]
		if token == "" || seenTokens[token] {
			continue
		}
		seenTokens[token] = true
		pages++
		payload := map[string]any{
			"context": map[string]any{
				"client": map[string]any{"clientName": "WEB", "clientVersion": clientVersion},
			},
			"continuation": token,
		}
		var page any
		if err := postJSON(ctx, "https://www.youtube.com/youtubei/v1/browse?key="+url.QueryEscape(apiKey), payload, &page, nil); err != nil {
			return nil, err
		}
		out = append(out, extractYouTubeVideos(page)...)
		tokens = append(tokens, continuationTokens(page)...)
	}
	return dedupeVideos(out), nil
}

func extractYouTubeVideos(root any) []Video {
	var out []Video
	walk(root, func(m map[string]any) {
		rich, ok := asMap(m["richItemRenderer"])
		if ok {
			content, ok := asMap(rich["content"])
			if !ok {
				return
			}
			lockup, ok := asMap(content["lockupViewModel"])
			if !ok {
				return
			}
			title := digString(lockup, "metadata", "lockupMetadataViewModel", "title", "content")
			id := digString(lockup, "rendererContext", "commandContext", "onTap", "innertubeCommand", "watchEndpoint", "videoId")
			if id != "" && title != "" {
				out = append(out, Video{ID: id, Title: title, Source: "youtube"})
			}
			return
		}
		video, ok := asMap(m["videoRenderer"])
		if ok {
			id := stringField(video, "videoId")
			title := textField(video["title"])
			if id != "" && title != "" {
				out = append(out, Video{ID: id, Title: title, Source: "youtube"})
			}
		}
	})
	return out
}

func continuationToken(root any) string {
	tokens := continuationTokens(root)
	if len(tokens) == 0 {
		return ""
	}
	return tokens[0]
}

func continuationTokens(root any) []string {
	var tokens []string
	walk(root, func(m map[string]any) {
		cmd, ok := asMap(m["continuationCommand"])
		if !ok {
			return
		}
		if s, ok := cmd["token"].(string); ok {
			tokens = append(tokens, s)
		}
	})
	sort.Strings(tokens)
	return tokens
}

func dedupeVideos(videos []Video) []Video {
	seen := map[string]bool{}
	out := make([]Video, 0, len(videos))
	for _, v := range videos {
		if seen[v.ID] {
			continue
		}
		seen[v.ID] = true
		out = append(out, v)
	}
	return out
}

var (
	atwikiRowRe  = regexp.MustCompile(`^\|([^|]+)\|(.+)\|$`)
	atwikiLinkRe = regexp.MustCompile(`\[\[([^>\]]+)>>([^\]]+)\]\]`)
	youtubeIDRe  = regexp.MustCompile(`[?&]v=([A-Za-z0-9_-]+)`)
	twitchIDRe   = regexp.MustCompile(`/videos/(\d+)`)
)

func parseAtWikiSeed(path string) ([]Entry, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var out []Entry
	for _, line := range strings.Split(string(b), "\n") {
		m := atwikiRowRe.FindStringSubmatch(strings.TrimSpace(line))
		if len(m) < 3 {
			continue
		}
		date := strings.TrimSpace(m[1])
		if date == "~配信日" || date == "" {
			continue
		}
		link := atwikiLinkRe.FindStringSubmatch(m[2])
		if len(link) < 3 {
			continue
		}
		title := cleanTitle(link[1])
		url := html.UnescapeString(link[2])
		e := Entry{
			Key:    keyFor(title, episode(title)),
			Date:   date,
			Title:  title,
			Source: "seed",
		}
		if id := firstSubmatch(youtubeIDRe, url); id != "" {
			e.YouTubeID = id
			e.Source = "youtube"
		}
		if id := firstSubmatch(twitchIDRe, url); id != "" {
			e.TwitchID = id
			if e.Source == "seed" {
				e.Source = "twitch"
			}
		}
		out = append(out, e)
	}
	return out, nil
}

func firstSubmatch(re *regexp.Regexp, s string) string {
	m := re.FindStringSubmatch(s)
	if len(m) < 2 {
		return ""
	}
	return m[1]
}

func postJSON(ctx context.Context, url string, payload any, dst any, headers map[string]string) error {
	b, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(b))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
	}
	defer res.Body.Close()
	if res.StatusCode < 200 || res.StatusCode >= 300 {
		return fmt.Errorf("POST %s: %s", url, res.Status)
	}
	return json.NewDecoder(res.Body).Decode(dst)
}

func get(ctx context.Context, url string, headers map[string]string) (string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return "", err
	}
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		return "", err
	}
	defer res.Body.Close()
	if res.StatusCode < 200 || res.StatusCode >= 300 {
		return "", fmt.Errorf("GET %s: %s", url, res.Status)
	}
	b, err := io.ReadAll(res.Body)
	if err != nil {
		return "", err
	}
	return string(b), nil
}

func extractJSON(s, prefix string) (string, error) {
	i := strings.Index(s, prefix)
	if i < 0 {
		return "", fmt.Errorf("prefix not found: %s", prefix)
	}
	start := strings.IndexByte(s[i:], '{')
	if start < 0 {
		return "", fmt.Errorf("json start not found")
	}
	start += i
	depth := 0
	inString := false
	escaped := false
	for j := start; j < len(s); j++ {
		c := s[j]
		if inString {
			if escaped {
				escaped = false
				continue
			}
			if c == '\\' {
				escaped = true
				continue
			}
			if c == '"' {
				inString = false
			}
			continue
		}
		switch c {
		case '"':
			inString = true
		case '{':
			depth++
		case '}':
			depth--
			if depth == 0 {
				return s[start : j+1], nil
			}
		}
	}
	return "", fmt.Errorf("json end not found")
}

func walk(v any, visit func(map[string]any)) {
	switch x := v.(type) {
	case map[string]any:
		visit(x)
		for _, child := range x {
			walk(child, visit)
		}
	case []any:
		for _, child := range x {
			walk(child, visit)
		}
	}
}

func asMap(v any) (map[string]any, bool) {
	m, ok := v.(map[string]any)
	return m, ok
}

func digString(root map[string]any, path ...string) string {
	var cur any = root
	for _, p := range path {
		m, ok := cur.(map[string]any)
		if !ok {
			return ""
		}
		cur = m[p]
	}
	s, _ := cur.(string)
	return s
}

func stringField(root map[string]any, key string) string {
	s, _ := root[key].(string)
	return s
}

func textField(v any) string {
	m, ok := asMap(v)
	if !ok {
		return ""
	}
	if s, ok := m["simpleText"].(string); ok {
		return s
	}
	runs, ok := m["runs"].([]any)
	if !ok {
		return ""
	}
	var b strings.Builder
	for _, run := range runs {
		rm, ok := asMap(run)
		if !ok {
			continue
		}
		if s, ok := rm["text"].(string); ok {
			b.WriteString(s)
		}
	}
	return b.String()
}

func firstMatch(s, expr string) string {
	m := regexp.MustCompile(expr).FindStringSubmatch(s)
	if len(m) < 2 {
		return ""
	}
	return m[1]
}

func wanted(title, include, season string) bool {
	if include != "" && !strings.Contains(title, include) {
		return false
	}
	if season != "" && !strings.Contains(strings.ToLower(title), strings.ToLower(season)) {
		return false
	}
	return true
}

var (
	bracketPrefix       = regexp.MustCompile(`^【[^】]*】\s*`)
	episodePartSuffixRe = regexp.MustCompile(`\s*#\d+[-ー－―]?([①-⑳])\s*$`)
	episodeSuffix       = regexp.MustCompile(`\s*#\d+(?:[-ー－―]?[①-⑳0-9]+)?\s*$`)
	episodeRe           = regexp.MustCompile(`(?:#|^)(\d+)(?:[.．])?`)
	partSuffixRe        = regexp.MustCompile(`([①-⑳])\s*$`)
	spaceRe             = regexp.MustCompile(`\s+`)
)

func cleanTitle(title string) string {
	title = html.UnescapeString(title)
	title = strings.TrimSpace(title)
	title = bracketPrefix.ReplaceAllString(title, "")
	if m := episodePartSuffixRe.FindStringSubmatch(title); len(m) >= 2 {
		title = episodePartSuffixRe.ReplaceAllString(title, m[1])
	}
	title = episodeSuffix.ReplaceAllString(title, "")
	title = strings.TrimSpace(title)
	title = spaceRe.ReplaceAllString(title, " ")
	return title
}

func episode(title string) int {
	title = html.UnescapeString(title)
	title = strings.TrimSpace(title)
	title = bracketPrefix.ReplaceAllString(title, "")
	m := episodeRe.FindStringSubmatch(title)
	if len(m) < 2 {
		return 0
	}
	return mustAtoi(m[1])
}

func keyFor(title string, ep int) string {
	if ep != 0 {
		if part := titlePart(title); part != "" {
			return fmt.Sprintf("ep:%03d:%s", ep, part)
		}
		return fmt.Sprintf("ep:%03d", ep)
	}
	return "title:" + normalizeTitle(title)
}

func titlePart(title string) string {
	title = cleanTitle(title)
	m := partSuffixRe.FindStringSubmatch(title)
	if len(m) < 2 {
		return ""
	}
	return fmt.Sprintf("part:%02d", circledNumber(m[1]))
}

func circledNumber(s string) int {
	switch s {
	case "①":
		return 1
	case "②":
		return 2
	case "③":
		return 3
	case "④":
		return 4
	case "⑤":
		return 5
	case "⑥":
		return 6
	case "⑦":
		return 7
	case "⑧":
		return 8
	case "⑨":
		return 9
	case "⑩":
		return 10
	case "⑪":
		return 11
	case "⑫":
		return 12
	case "⑬":
		return 13
	case "⑭":
		return 14
	case "⑮":
		return 15
	case "⑯":
		return 16
	case "⑰":
		return 17
	case "⑱":
		return 18
	case "⑲":
		return 19
	case "⑳":
		return 20
	default:
		return 0
	}
}

func normalizeTitle(title string) string {
	title = cleanTitle(title)
	title = strings.ToLower(title)
	title = strings.ReplaceAll(title, " ", "")
	title = strings.ReplaceAll(title, "　", "")
	return title
}

func atwikiText(s string) string {
	return strings.ReplaceAll(s, "|", "｜")
}

func isDate(s string) bool {
	_, err := time.Parse("2006/01/02", s)
	return err == nil
}

func mustAtoi(s string) int {
	n, _ := strconv.Atoi(s)
	return n
}

func jst() *time.Location {
	loc, err := time.LoadLocation("Asia/Tokyo")
	if err != nil {
		return time.FixedZone("JST", 9*60*60)
	}
	return loc
}
