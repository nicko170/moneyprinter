package youtube

import (
	"context"
	"fmt"
	"os"
	"strings"
	"time"

	"golang.org/x/oauth2"
	"golang.org/x/oauth2/google"
	"google.golang.org/api/option"
	yt "google.golang.org/api/youtube/v3"
)

// OAuthConfig holds the credentials needed to authenticate with YouTube.
type OAuthConfig struct {
	ClientID     string
	ClientSecret string
	RefreshToken string
}

// Metadata describes the video for YouTube.
type Metadata struct {
	Title       string
	Description string
	Tags        []string
	Privacy   string // "public", "unlisted", "private"
	ChannelID string // target channel ID (for multi-channel accounts)
}

// Client wraps the YouTube Data API v3 service.
type Client struct {
	service *yt.Service
}

// NewClient creates a YouTube API client using a stored OAuth2 refresh token.
func NewClient(ctx context.Context, cfg OAuthConfig) (*Client, error) {
	if cfg.RefreshToken == "" {
		return nil, fmt.Errorf("no YouTube refresh token configured")
	}

	oauthCfg := &oauth2.Config{
		ClientID:     cfg.ClientID,
		ClientSecret: cfg.ClientSecret,
		Endpoint:     google.Endpoint,
		Scopes:       []string{yt.YoutubeScope, yt.YoutubeForceSslScope},
	}

	token := &oauth2.Token{RefreshToken: cfg.RefreshToken}
	httpClient := oauthCfg.Client(ctx, token)

	service, err := yt.NewService(ctx, option.WithHTTPClient(httpClient))
	if err != nil {
		return nil, fmt.Errorf("creating youtube service: %w", err)
	}

	return &Client{service: service}, nil
}

// AuthURL returns the OAuth2 authorization URL for the user to visit.
func AuthURL(clientID, clientSecret, redirectURL string) string {
	cfg := &oauth2.Config{
		ClientID:     clientID,
		ClientSecret: clientSecret,
		Endpoint:     google.Endpoint,
		RedirectURL:  redirectURL,
		Scopes:       []string{yt.YoutubeScope, yt.YoutubeForceSslScope},
	}
	return cfg.AuthCodeURL("state-token", oauth2.AccessTypeOffline, oauth2.SetAuthURLParam("prompt", "consent"))
}

// ExchangeCode exchanges an authorization code for tokens. Returns the refresh token.
func ExchangeCode(ctx context.Context, clientID, clientSecret, redirectURL, code string) (string, error) {
	cfg := &oauth2.Config{
		ClientID:     clientID,
		ClientSecret: clientSecret,
		Endpoint:     google.Endpoint,
		RedirectURL:  redirectURL,
		Scopes:       []string{yt.YoutubeScope, yt.YoutubeForceSslScope},
	}

	token, err := cfg.Exchange(ctx, code)
	if err != nil {
		return "", fmt.Errorf("exchanging code: %w", err)
	}

	if token.RefreshToken == "" {
		return "", fmt.Errorf("no refresh token returned — try revoking app access at https://myaccount.google.com/permissions and re-authorizing")
	}

	return token.RefreshToken, nil
}

// Upload uploads a video file to YouTube and returns the video ID.
func (c *Client) Upload(ctx context.Context, videoPath string, meta Metadata) (string, error) {
	file, err := os.Open(videoPath)
	if err != nil {
		return "", fmt.Errorf("opening video: %w", err)
	}
	defer file.Close()

	title := meta.Title
	if !strings.Contains(strings.ToLower(title), "#shorts") {
		title += " #Shorts"
	}

	description := meta.Description
	if len(meta.Tags) > 0 {
		hashtags := make([]string, len(meta.Tags))
		for i, t := range meta.Tags {
			if !strings.HasPrefix(t, "#") {
				hashtags[i] = "#" + t
			} else {
				hashtags[i] = t
			}
		}
		description += "\n\n" + strings.Join(hashtags, " ")
	}
	if !strings.Contains(strings.ToLower(description), "#shorts") {
		description += " #Shorts"
	}

	privacy := meta.Privacy
	if privacy == "" {
		privacy = "public"
	}

	snippet := &yt.VideoSnippet{
		Title:       title,
		Description: description,
		Tags:        append(meta.Tags, "Shorts"),
		CategoryId:  "22", // People & Blogs
	}
	if meta.ChannelID != "" {
		snippet.ChannelId = meta.ChannelID
	}

	video := &yt.Video{
		Snippet: snippet,
		Status: &yt.VideoStatus{
			PrivacyStatus: privacy,
			MadeForKids:   false,
		},
	}

	call := c.service.Videos.Insert([]string{"snippet", "status"}, video)
	call.Media(file)
	call.Context(ctx)

	resp, err := call.Do()
	if err != nil {
		return "", fmt.Errorf("uploading to youtube: %w", err)
	}

	return resp.Id, nil
}

// Channel represents a YouTube channel the user has access to.
type Channel struct {
	ID    string `json:"id"`
	Title string `json:"title"`
	Thumb string `json:"thumbnail,omitempty"`
}

// ListChannels returns all channels the authenticated user can upload to.
func (c *Client) ListChannels(ctx context.Context) ([]Channel, error) {
	call := c.service.Channels.List([]string{"snippet"}).Mine(true).Context(ctx)
	resp, err := call.Do()
	if err != nil {
		return nil, fmt.Errorf("listing channels: %w", err)
	}

	var channels []Channel
	for _, ch := range resp.Items {
		thumb := ""
		if ch.Snippet.Thumbnails != nil && ch.Snippet.Thumbnails.Default != nil {
			thumb = ch.Snippet.Thumbnails.Default.Url
		}
		channels = append(channels, Channel{
			ID:    ch.Id,
			Title: ch.Snippet.Title,
			Thumb: thumb,
		})
	}
	return channels, nil
}

// Comment represents a YouTube comment thread.
type Comment struct {
	ID          string    `json:"id"`
	VideoID     string    `json:"videoId"`
	Author      string    `json:"author"`
	AuthorURL   string    `json:"authorUrl,omitempty"`
	Text        string    `json:"text"`
	LikeCount   int64     `json:"likeCount"`
	PublishedAt time.Time `json:"publishedAt"`
	ReplyCount  int64     `json:"replyCount"`
}

// ListNewComments returns top-level comment threads for a video, newest first.
func (c *Client) ListNewComments(ctx context.Context, videoID string, maxResults int64) ([]Comment, error) {
	if maxResults <= 0 {
		maxResults = 50
	}
	call := c.service.CommentThreads.List([]string{"snippet"}).
		VideoId(videoID).
		Order("time").
		MaxResults(maxResults).
		Context(ctx)

	resp, err := call.Do()
	if err != nil {
		return nil, fmt.Errorf("listing comments: %w", err)
	}

	var comments []Comment
	for _, item := range resp.Items {
		s := item.Snippet.TopLevelComment.Snippet
		published, _ := time.Parse(time.RFC3339, s.PublishedAt)
		comments = append(comments, Comment{
			ID:          item.Snippet.TopLevelComment.Id,
			VideoID:     videoID,
			Author:      s.AuthorDisplayName,
			AuthorURL:   s.AuthorChannelUrl,
			Text:        s.TextOriginal,
			LikeCount:   s.LikeCount,
			PublishedAt: published,
			ReplyCount:  int64(item.Snippet.TotalReplyCount),
		})
	}
	return comments, nil
}

// ReplyToComment posts a reply to a comment and returns the reply ID.
func (c *Client) ReplyToComment(ctx context.Context, parentCommentID, text string) (string, error) {
	comment := &yt.Comment{
		Snippet: &yt.CommentSnippet{
			ParentId:     parentCommentID,
			TextOriginal: text,
		},
	}

	call := c.service.Comments.Insert([]string{"snippet"}, comment).Context(ctx)
	resp, err := call.Do()
	if err != nil {
		return "", fmt.Errorf("replying to comment: %w", err)
	}
	return resp.Id, nil
}

// VideoURL returns the YouTube Shorts URL for a video ID.
func VideoURL(videoID string) string {
	return "https://youtube.com/shorts/" + videoID
}
