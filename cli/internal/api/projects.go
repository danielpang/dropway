package api

import (
	"context"
	"encoding/json"
	"net/url"
	"strconv"
)

type ProjectCommentsQuery struct {
	VersionID, Since, Until, Type, Cursor string
	Limit                                 int
}
type ProjectsClient interface {
	ListProjects(context.Context) (json.RawMessage, error)
	ProjectComments(context.Context, string, ProjectCommentsQuery) (json.RawMessage, error)
	ProjectThread(context.Context, string, string, string, int) (json.RawMessage, error)
}

func (c *HTTPClient) ListProjects(ctx context.Context) (json.RawMessage, error) {
	var out json.RawMessage
	err := c.getJSON(ctx, "/v1/project-rooms", &out)
	return out, err
}
func projectCommentsPath(roomID string, q ProjectCommentsQuery) string {
	v := url.Values{"limit": {strconv.Itoa(q.Limit)}}
	for k, value := range map[string]string{"version_id": q.VersionID, "created_after": q.Since, "created_before": q.Until, "type": q.Type, "cursor": q.Cursor} {
		if value != "" {
			v.Set(k, value)
		}
	}
	return "/v1/project-rooms/" + url.PathEscape(roomID) + "/threads?" + v.Encode()
}
func (c *HTTPClient) ProjectComments(ctx context.Context, id string, q ProjectCommentsQuery) (json.RawMessage, error) {
	var out json.RawMessage
	err := c.getJSON(ctx, projectCommentsPath(id, q), &out)
	return out, err
}
func (c *HTTPClient) ProjectThread(ctx context.Context, id, cursor, revisions string, limit int) (json.RawMessage, error) {
	v := url.Values{"limit": {strconv.Itoa(limit)}}
	if cursor != "" {
		v.Set("cursor", cursor)
	}
	if revisions != "" {
		v.Set("revisions_cursor", revisions)
	}
	var out json.RawMessage
	err := c.getJSON(ctx, "/v1/project-rooms/threads/"+url.PathEscape(id)+"?"+v.Encode(), &out)
	return out, err
}
