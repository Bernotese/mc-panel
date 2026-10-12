package upstream

import (
	"context"
	"fmt"
)

var errHangarPending = fmt.Errorf("%w: the hangar client follows in task 8", ErrUnavailable)

func (c *Client) hangarSearch(context.Context, SearchQuery) (SearchResult, error) {
	return SearchResult{}, errHangarPending
}

func (c *Client) hangarProject(context.Context, string) (Project, error) {
	return Project{}, errHangarPending
}

func (c *Client) hangarVersions(context.Context, string, string, bool) ([]Version, error) {
	return nil, errHangarPending
}

func (c *Client) hangarVersion(context.Context, string, string) (Version, error) {
	return Version{}, errHangarPending
}
