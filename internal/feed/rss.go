package feed

import (
	"bytes"
	"context"
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"time"

	"portwatch/internal/config"
)

const maxFeedBytes = 2 << 20

var portWatchPath = regexp.MustCompile(`(?:^|/)news/([a-z0-9][a-z0-9-]{0,80})(?:\.html)?/?$`)

type Article struct {
	ID          string
	Title       string
	Summary     string
	URL         string
	PublishedAt time.Time
}

type Client struct {
	http *http.Client
}

func NewClient() *Client {
	return &Client{http: &http.Client{
		Timeout: 10 * time.Second,
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			if req.URL.Scheme != "https" || len(via) >= 10 {
				return errors.New("rejected feed redirect")
			}
			return nil
		},
	}}
}

type rssItem struct {
	GUID    string `xml:"guid"`
	Title   string `xml:"title"`
	Summary string `xml:"description"`
	Link    string `xml:"link"`
	Date    string `xml:"pubDate"`
}

func (client *Client) Fetch(ctx context.Context, source config.Source) ([]Article, error) {
	body, address, err := client.get(ctx, source.FeedURL, "application/rss+xml, application/xml, text/xml")
	if err != nil {
		return nil, fmt.Errorf("fetch RSS: %w", err)
	}
	return parse(body, source.Kind, address)
}

func (client *Client) get(ctx context.Context, address, accept string) ([]byte, *url.URL, error) {
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, address, nil)
	if err != nil {
		return nil, nil, err
	}
	request.Header.Set("User-Agent", "PortWatchBot/1.0")
	request.Header.Set("Accept", accept)
	response, err := client.http.Do(request)
	if err != nil {
		return nil, nil, err
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return nil, nil, fmt.Errorf("HTTP status %d", response.StatusCode)
	}
	body, err := io.ReadAll(io.LimitReader(response.Body, maxFeedBytes+1))
	if err != nil {
		return nil, nil, fmt.Errorf("read response: %w", err)
	}
	if len(body) > maxFeedBytes {
		return nil, nil, errors.New("response exceeds 2 MiB")
	}
	return body, response.Request.URL, nil
}

func parse(body []byte, kind string, base *url.URL) ([]Article, error) {
	var document struct {
		XMLName xml.Name `xml:"rss"`
		Channel *struct {
			Items []rssItem `xml:"item"`
		} `xml:"channel"`
	}
	decoder := xml.NewDecoder(bytes.NewReader(body))
	if err := decoder.Decode(&document); err != nil {
		return nil, fmt.Errorf("decode RSS: %w", err)
	}
	if document.Channel == nil {
		return nil, errors.New("RSS has no channel")
	}
	for {
		token, err := decoder.Token()
		if errors.Is(err, io.EOF) {
			break
		}
		switch trailing := token.(type) {
		case xml.Comment, xml.ProcInst:
			continue
		case xml.CharData:
			if len(bytes.TrimSpace(trailing)) == 0 {
				continue
			}
		}
		return nil, errors.New("unexpected data after RSS document")
	}

	items := document.Channel.Items
	articles := make([]Article, 0, len(items))
	for _, item := range items {
		if article, ok := normalize(item, kind, base); ok {
			articles = append(articles, article)
		}
	}
	if len(items) > 0 && len(articles) == 0 {
		return nil, errors.New("RSS contains no usable articles")
	}
	return articles, nil
}

func normalize(item rssItem, kind string, base *url.URL) (Article, bool) {
	title := strings.TrimSpace(item.Title)
	link := strings.TrimSpace(item.Link)
	address, err := base.Parse(link)
	if err != nil || title == "" || link == "" || address.Hostname() == "" || address.User != nil ||
		(address.Scheme != "https" && address.Scheme != "http") {
		return Article{}, false
	}
	link = address.String()
	if len(link) > 2048 {
		return Article{}, false
	}

	id := strings.TrimSpace(item.GUID)
	if kind == config.PortWatch {
		match := portWatchPath.FindStringSubmatch(address.Path)
		if match == nil {
			return Article{}, false
		}
		id = match[1]
	}
	if id == "" {
		id = link
	}
	var published time.Time
	for _, layout := range []string{time.RFC1123, time.RFC1123Z, time.RFC822, time.RFC822Z, time.RFC3339} {
		parsed, err := time.Parse(layout, strings.TrimSpace(item.Date))
		if err == nil {
			published = parsed
			break
		}
	}
	return Article{ID: id, Title: title, Summary: strings.TrimSpace(item.Summary), URL: link, PublishedAt: published}, true
}
