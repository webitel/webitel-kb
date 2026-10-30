package grpc

import (
	"errors"
	"strings"
	"testing"

	"buf.build/go/protovalidate"
	"google.golang.org/protobuf/proto"

	"github.com/webitel/webitel-kb/api/kb"
)

func TestInputRules(t *testing.T) {
	validator, err := protovalidate.New()
	if err != nil {
		t.Fatalf("validator: %v", err)
	}

	atLimit, overLimit := strings.Repeat("я", 255), strings.Repeat("я", 256)

	space := func(in *kb.InputSpace) *kb.CreateSpaceRequest {
		in.Language = "uk"

		return &kb.CreateSpaceRequest{Input: in}
	}
	article := func(in *kb.InputArticle) *kb.CreateArticleRequest {
		in.SpaceId = 1

		return &kb.CreateArticleRequest{Input: in}
	}

	tests := []struct {
		name      string
		msg       proto.Message
		wantField string
		wantRule  string
	}{
		{name: "space name at the limit", msg: space(&kb.InputSpace{Name: atLimit})},
		{name: "space name over the limit", msg: space(&kb.InputSpace{Name: overLimit}), wantField: "input.name", wantRule: "string.max_len"},
		{name: "space name of emoji at the limit", msg: space(&kb.InputSpace{Name: strings.Repeat("🙂", 255)})},
		{name: "space name left out", msg: &kb.UpdateSpaceRequest{Id: 1, Input: &kb.InputSpace{}}},
		{name: "space update over the limit", msg: &kb.UpdateSpaceRequest{Id: 1, Input: &kb.InputSpace{Name: overLimit}}, wantField: "input.name", wantRule: "string.max_len"},
		{name: "subject at the limit", msg: article(&kb.InputArticle{Subject: atLimit})},
		{name: "subject over the limit", msg: article(&kb.InputArticle{Subject: overLimit}), wantField: "input.subject", wantRule: "string.max_len"},
		{name: "subject left out", msg: &kb.UpdateArticleRequest{Input: &kb.InputArticle{}}},
		{name: "article update over the limit", msg: &kb.UpdateArticleRequest{Input: &kb.InputArticle{Subject: overLimit}}, wantField: "input.subject", wantRule: "string.max_len"},

		{name: "space create without input", msg: &kb.CreateSpaceRequest{}, wantField: "input", wantRule: "required"},
		{name: "space create without language", msg: &kb.CreateSpaceRequest{Input: &kb.InputSpace{Name: "docs"}}, wantField: "input", wantRule: "kb.space.language_required"},
		{name: "space update without input", msg: &kb.UpdateSpaceRequest{Id: 1}, wantField: "input", wantRule: "required"},
		{name: "space update without language", msg: &kb.UpdateSpaceRequest{Id: 1, Input: &kb.InputSpace{State: kb.SpaceState_SPACE_STATE_INACTIVE}}},
		{name: "space update without id", msg: &kb.UpdateSpaceRequest{Input: &kb.InputSpace{}}, wantField: "id", wantRule: "int64.gt"},
		{name: "space unknown state", msg: space(&kb.InputSpace{State: 3}), wantField: "input.state", wantRule: "enum.defined_only"},
		{name: "space negative embedding model", msg: space(&kb.InputSpace{EmbeddingModelId: -1}), wantField: "input.embedding_model_id", wantRule: "int64.gte"},
		{name: "space negative reranker model", msg: space(&kb.InputSpace{RerankerModelId: -1}), wantField: "input.reranker_model_id", wantRule: "int64.gte"},
		{name: "space zero team", msg: space(&kb.InputSpace{TeamIds: []int64{4, 0}}), wantField: "input.team_ids[1]", wantRule: "int64.gt"},
		{name: "space teams", msg: space(&kb.InputSpace{TeamIds: []int64{4, 5}})},
		{name: "space locate without id", msg: &kb.LocateSpaceRequest{}, wantField: "id", wantRule: "int64.gt"},
		{name: "space delete with negative id", msg: &kb.DeleteSpaceRequest{Id: -1}, wantField: "id", wantRule: "int64.gt"},
		{name: "space list unknown state", msg: &kb.ListSpacesRequest{State: 9}, wantField: "state", wantRule: "enum.defined_only"},
		{name: "space list with lenient paging", msg: &kb.ListSpacesRequest{Size: -1, Page: -3}},
		{name: "space list language is the service's to check", msg: &kb.ListSpacesRequest{Language: "Ukrainian"}},

		{name: "article create without input", msg: &kb.CreateArticleRequest{}, wantField: "input", wantRule: "required"},
		{name: "article create without space", msg: &kb.CreateArticleRequest{Input: &kb.InputArticle{Subject: "a"}}, wantField: "input", wantRule: "kb.article.space_required"},
		{name: "article update without input", msg: &kb.UpdateArticleRequest{Etag: "x"}, wantField: "input", wantRule: "required"},
		{name: "article unknown type", msg: article(&kb.InputArticle{Type: 9}), wantField: "input.type", wantRule: "enum.defined_only"},
		{name: "article negative state", msg: article(&kb.InputArticle{State: -1}), wantField: "input.state", wantRule: "enum.defined_only"},
		{name: "article known codes", msg: article(&kb.InputArticle{Type: kb.ArticleType_FAQ, State: kb.ArticleState_DRAFT})},
		{name: "article negative parent", msg: article(&kb.InputArticle{ParentId: -1}), wantField: "input.parent_id", wantRule: "int64.gte"},
		{name: "article list unknown type", msg: &kb.ListArticlesRequest{Type: 5}, wantField: "type", wantRule: "enum.defined_only"},
		{name: "article list unknown state", msg: &kb.ListArticlesRequest{State: 5}, wantField: "state", wantRule: "enum.defined_only"},
		{name: "article list unknown tag match", msg: &kb.ListArticlesRequest{TagMatch: 5}, wantField: "tag_match", wantRule: "enum.defined_only"},
		{name: "article move to negative parent", msg: &kb.MoveArticleRequest{Etag: "x", NewParentId: -1}, wantField: "new_parent_id", wantRule: "int64.gte"},
		{name: "article move to top level", msg: &kb.MoveArticleRequest{Etag: "x"}},
		{name: "article move to negative space", msg: &kb.MoveArticleRequest{Etag: "x", NewSpaceId: -1}, wantField: "new_space_id", wantRule: "int64.gte"},
		{name: "article move into another space", msg: &kb.MoveArticleRequest{Etag: "x", NewParentId: 4, NewSpaceId: 9}},
		{name: "children without parent", msg: &kb.ListChildrenRequest{}, wantField: "id", wantRule: "int64.gt"},
		{name: "ancestors without article", msg: &kb.ListAncestorsRequest{}, wantField: "id", wantRule: "int64.gt"},
		{name: "tree without space", msg: &kb.GetTreeRequest{}, wantField: "space_id", wantRule: "int64.gt"},

		{name: "versions without article", msg: &kb.ListVersionsRequest{}, wantField: "article_id", wantRule: "int64.gt"},
		{name: "version without number", msg: &kb.GetVersionRequest{ArticleId: 1}, wantField: "version_number", wantRule: "int32.gt"},
		{name: "restore note at the limit", msg: &kb.RestoreVersionRequest{ArticleId: 1, VersionNumber: 1, Notes: strings.Repeat("я", 50)}},
		{name: "restore note over the limit", msg: &kb.RestoreVersionRequest{ArticleId: 1, VersionNumber: 1, Notes: strings.Repeat("я", 51)}, wantField: "notes", wantRule: "string.max_len"},
		{name: "restore without article", msg: &kb.RestoreVersionRequest{VersionNumber: 1}, wantField: "article_id", wantRule: "int64.gt"},

		{name: "model create without input", msg: &kb.CreateModelRequest{}, wantField: "input", wantRule: "required"},
		{name: "model create without type", msg: &kb.CreateModelRequest{Input: &kb.InputEmbeddingModel{Name: "m"}}, wantField: "input", wantRule: "kb.model.type_required"},
		{name: "model create with unknown type", msg: &kb.CreateModelRequest{Input: &kb.InputEmbeddingModel{Type: "generator"}}, wantField: "input.type", wantRule: "string.in"},
		{name: "model create reranker", msg: &kb.CreateModelRequest{Input: &kb.InputEmbeddingModel{Type: "reranker"}}},
		{name: "model update without type", msg: &kb.UpdateModelRequest{Id: 1, Input: &kb.InputEmbeddingModel{Name: "m"}}},
		{name: "model update with unknown type", msg: &kb.UpdateModelRequest{Id: 1, Input: &kb.InputEmbeddingModel{Type: "generator"}}, wantField: "input.type", wantRule: "string.in"},
		{name: "model update without input", msg: &kb.UpdateModelRequest{Id: 1}, wantField: "input", wantRule: "required"},
		{name: "model validate without id", msg: &kb.ValidateModelRequest{}, wantField: "id", wantRule: "int64.gt"},
		{name: "model locate without id", msg: &kb.LocateModelRequest{}, wantField: "id", wantRule: "int64.gt"},
		{name: "model delete without id", msg: &kb.DeleteModelRequest{}, wantField: "id", wantRule: "int64.gt"},

		{name: "resolve at the limit", msg: &kb.ResolveRequest{Ids: make([]int64, 5000)}},
		{name: "resolve over the limit", msg: &kb.ResolveRequest{Ids: make([]int64, 5001)}, wantField: "ids", wantRule: "repeated.max_items"},
		{name: "search unknown tag match", msg: &kb.SearchRequest{TagMatch: 7}, wantField: "tag_match", wantRule: "enum.defined_only"},
		{name: "semantic blank query without spaces", msg: &kb.SemanticSearchRequest{Query: "  "}},
		{name: "semantic query without spaces", msg: &kb.SemanticSearchRequest{Query: "vpn"}, wantRule: "kb.retrieval.space_required"},
		{name: "semantic query with spaces", msg: &kb.SemanticSearchRequest{Query: "vpn", SpaceIds: []int64{1}, TopK: 500}},
		{name: "semantic unknown tag match", msg: &kb.SemanticSearchRequest{SpaceIds: []int64{1}, TagMatch: 7}, wantField: "tag_match", wantRule: "enum.defined_only"},
		{name: "suggest blank message without spaces", msg: &kb.SuggestRequest{CustomerMessage: " "}},
		{name: "suggest message without spaces or team", msg: &kb.SuggestRequest{CustomerMessage: "vpn"}, wantRule: "kb.retrieval.space_required"},
		{name: "suggest message with a team", msg: &kb.SuggestRequest{CustomerMessage: "vpn", TeamId: 3}},
		{name: "suggest message with spaces", msg: &kb.SuggestRequest{CustomerMessage: "vpn", SpaceIds: []int64{1}}},
		{name: "menu without space", msg: &kb.MenuRequest{DepthLimit: 9}, wantField: "space_id", wantRule: "int64.gt"},
		{name: "menu with lenient depth", msg: &kb.MenuRequest{SpaceId: 1, DepthLimit: 9}},
		{name: "tags without space", msg: &kb.SuggestTagsRequest{Size: 6000}, wantField: "space_id", wantRule: "int64.gt"},

		{name: "attach without file", msg: &kb.AttachFileRequest{ArticleEtag: "7"}, wantField: "file_id", wantRule: "int64.gt"},
		{name: "detach without file", msg: &kb.DeleteFileRequest{ArticleEtag: "7"}, wantField: "id", wantRule: "int64.gt"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := validator.Validate(tt.msg)
			if tt.wantRule == "" {
				if err != nil {
					t.Fatalf("Validate: %v, want it accepted", err)
				}

				return
			}

			var verr *protovalidate.ValidationError
			if !errors.As(err, &verr) || len(verr.Violations) != 1 {
				t.Fatalf("Validate = %v, want one violation", err)
			}

			got := verr.Violations[0].Proto
			if path := protovalidate.FieldPathString(got.GetField()); path != tt.wantField || got.GetRuleId() != tt.wantRule {
				t.Fatalf("violation = %s %s, want %s %s", path, got.GetRuleId(), tt.wantField, tt.wantRule)
			}
		})
	}
}
