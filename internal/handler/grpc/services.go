package grpc

import (
	kb "github.com/webitel/webitel-kb/api/kb"
)

type SpacesService struct {
	kb.UnimplementedSpacesServer
}

func NewSpacesService() *SpacesService {
	return &SpacesService{}
}

type EmbeddingModelsService struct {
	kb.UnimplementedEmbeddingModelsServer
}

func NewEmbeddingModelsService() *EmbeddingModelsService {
	return &EmbeddingModelsService{}
}

type ArticlesService struct {
	kb.UnimplementedArticlesServer
}

func NewArticlesService() *ArticlesService {
	return &ArticlesService{}
}

type VersionsService struct {
	kb.UnimplementedVersionsServer
}

func NewVersionsService() *VersionsService {
	return &VersionsService{}
}

type TagsService struct {
	kb.UnimplementedTagsServer
}

func NewTagsService() *TagsService {
	return &TagsService{}
}

type AttachmentsService struct {
	kb.UnimplementedAttachmentsServer
}

func NewAttachmentsService() *AttachmentsService {
	return &AttachmentsService{}
}

type RetrievalService struct {
	kb.UnimplementedRetrievalServer
}

func NewRetrievalService() *RetrievalService {
	return &RetrievalService{}
}
