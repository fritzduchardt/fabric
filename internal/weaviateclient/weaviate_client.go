package weaviateclient

import (
	"os"
	"sync"

	"github.com/weaviate/weaviate-go-client/v5/weaviate"
)

var (
	once           sync.Once
	weaviateClient *weaviate.Client
	initErr        error
)

func GetClient() (*weaviate.Client, error) {
	once.Do(func() {
		cfg := weaviate.Config{
			Host:   os.Getenv("WEAVIATE_URL"),
			Scheme: "http",
		}
		weaviateClient, initErr = weaviate.NewClient(cfg)
	})
	return weaviateClient, initErr
}
