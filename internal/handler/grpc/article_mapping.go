package grpc

import (
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/types/known/structpb"

	"github.com/webitel/webitel-go-kit/pkg/errors"

	"github.com/webitel/webitel-kb/api/kb"
	"github.com/webitel/webitel-kb/internal/etag"
	"github.com/webitel/webitel-kb/internal/model"
)

// articleFromInput takes the writable fields; the hierarchy moves through
// MoveArticle and the body travels separately.
func articleFromInput(in *kb.InputArticle) *model.Article {
	return &model.Article{
		SpaceID:  in.GetSpaceId(),
		ParentID: in.GetParentId(),
		Type:     int32(in.GetType()),
		Subject:  in.GetSubject(),
		Tags:     in.GetTags(),
		State:    int32(in.GetState()),
	}
}

// bodyFromInput renders the editor document as the JSON the store keeps; a
// missing document means the request carries no body.
func bodyFromInput(in *kb.InputArticle) ([]byte, error) {
	doc := in.GetBodyRichText()
	if doc == nil {
		return nil, nil
	}

	raw, err := protojson.Marshal(doc)
	if err != nil {
		return nil, errors.InvalidArgument(
			"the body is not a valid editor document",
			errors.WithID("kb.article.body_invalid"),
			errors.WithCause(err),
		)
	}

	return raw, nil
}

func articleToProto(in *model.Article) (*kb.Article, error) {
	tag, err := etag.Encode(etag.TypeArticle, in.ID, in.Ver)
	if err != nil {
		return nil, err
	}

	return &kb.Article{
		Id:                 in.ID,
		DomainId:           in.DomainID,
		Space:              lookupToProto(in.Space),
		ParentId:           in.ParentID,
		Depth:              in.Depth,
		Type:               kb.ArticleType(in.Type),
		Subject:            in.Subject,
		Tags:               in.Tags,
		State:              kb.ArticleState(in.State),
		IndexState:         kb.IndexState(in.IndexState),
		PublishedVersionId: in.PublishedVersionID,
		Ver:                in.Ver,
		CreatedAt:          unixMilli(in.CreatedAt),
		UpdatedAt:          unixMilli(in.UpdatedAt),
		CreatedBy:          lookupToProto(in.CreatedBy),
		UpdatedBy:          lookupToProto(in.UpdatedBy),
		Etag:               tag,
	}, nil
}

func articleListToProto(items []*model.Article, next bool) (*kb.ArticleList, error) {
	articles := make([]*kb.Article, 0, len(items))

	for _, item := range items {
		article, err := articleToProto(item)
		if err != nil {
			return nil, err
		}

		articles = append(articles, article)
	}

	return &kb.ArticleList{Items: articles, Next: next}, nil
}

func versionToProto(in *model.ArticleVersion) (*kb.ArticleVersion, error) {
	out := &kb.ArticleVersion{
		Id:            in.ID,
		ArticleId:     in.ArticleID,
		VersionNumber: in.VersionNumber,
		Subject:       in.Subject,
		BodyMarkdown:  in.BodyMarkdown,
		BodyPlain:     in.BodyPlain,
		RestoredFrom:  in.RestoredFrom,
		Notes:         in.Notes,
		CreatedAt:     unixMilli(in.CreatedAt),
		CreatedBy:     lookupToProto(in.CreatedBy),
	}

	// A narrow field set legitimately leaves the document out.
	if len(in.BodyRichText) == 0 {
		return out, nil
	}

	doc := new(structpb.Struct)
	if err := protojson.Unmarshal(in.BodyRichText, doc); err != nil {
		return nil, errors.Internal(
			"the stored body cannot be rendered",
			errors.WithID("kb.article.body_unreadable"),
			errors.WithCause(err),
		)
	}

	out.BodyRichText = doc

	return out, nil
}

func treeToProto(nodes []*model.TreeNode) []*kb.TreeNode {
	if len(nodes) == 0 {
		return nil
	}

	out := make([]*kb.TreeNode, 0, len(nodes))

	for _, node := range nodes {
		out = append(out, &kb.TreeNode{
			Id:       node.ID,
			Subject:  node.Subject,
			Type:     kb.ArticleType(node.Type),
			Depth:    node.Depth,
			Children: treeToProto(node.Children),
		})
	}

	return out
}
