package embedding

import "context"

// prefixed tells a query from a document by a prefix on the text, for models
// trained to read the task from there (e5).
type prefixed struct {
	Provider

	query, document string
}

func (p prefixed) Embed(ctx context.Context, req EmbedRequest) (EmbedResult, error) {
	prefix := p.document
	if req.Task == TaskQuery {
		prefix = p.query
	}

	texts := make([]string, len(req.Texts))
	for i, text := range req.Texts {
		texts[i] = prefix + text
	}

	req.Texts = texts

	return p.Provider.Embed(ctx, req)
}
