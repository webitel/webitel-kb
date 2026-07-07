package grpc

import (
	kb "github.com/webitel/webitel-kb/api/kb"
)

type SpacesServer struct {
	kb.UnimplementedSpacesServer
}

func NewSpacesServer() *SpacesServer {
	return &SpacesServer{}
}

type EmbeddingModelsServer struct {
	kb.UnimplementedEmbeddingModelsServer
}

func NewEmbeddingModelsServer() *EmbeddingModelsServer {
	return &EmbeddingModelsServer{}
}

type ArticlesServer struct {
	kb.UnimplementedArticlesServer
}

func NewArticlesServer() *ArticlesServer {
	return &ArticlesServer{}
}

type VersionsServer struct {
	kb.UnimplementedVersionsServer
}

func NewVersionsServer() *VersionsServer {
	return &VersionsServer{}
}

type TagsServer struct {
	kb.UnimplementedTagsServer
}

func NewTagsServer() *TagsServer {
	return &TagsServer{}
}

type AttachmentsServer struct {
	kb.UnimplementedAttachmentsServer
}

func NewAttachmentsServer() *AttachmentsServer {
	return &AttachmentsServer{}
}

type RetrievalServer struct {
	kb.UnimplementedRetrievalServer
}

func NewRetrievalServer() *RetrievalServer {
	return &RetrievalServer{}
}
