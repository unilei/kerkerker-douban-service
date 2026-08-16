package repository

import (
	"context"
	"fmt"
	"time"

	"go.mongodb.org/mongo-driver/v2/mongo"
	"go.mongodb.org/mongo-driver/v2/mongo/options"
	"go.mongodb.org/mongo-driver/v2/mongo/readpref"
)

// MongoStores 是一组共享同一 MongoDB 连接的持久存储。
// 通过 NewMongoStores 一次性创建，避免各 store 各自维护独立连接。
type MongoStores struct {
	client   *mongo.Client
	Movie    MovieStore
	ImageMap ImageMapStore
	Snapshot SnapshotStore
}

// NewMongoStores 连接到 MongoDB，确保索引，并返回共享同一 client 的全部 store。
// 任一步骤失败即返回错误，调用方应降级为 Redis-only 模式。
func NewMongoStores(ctx context.Context, uri, dbName string) (*MongoStores, error) {
	client, err := mongo.Connect(options.Client().ApplyURI(uri))
	if err != nil {
		return nil, fmt.Errorf("mongo connect: %w", err)
	}

	pingCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	if err := client.Ping(pingCtx, readpref.Primary()); err != nil {
		_ = client.Disconnect(ctx)
		return nil, fmt.Errorf("mongo ping: %w", err)
	}

	movie, err := newMongoMovieStoreWithClient(ctx, client, dbName)
	if err != nil {
		_ = client.Disconnect(ctx)
		return nil, err
	}
	imageMap, err := newMongoImageMapStoreWithClient(ctx, client, dbName)
	if err != nil {
		_ = client.Disconnect(ctx)
		return nil, err
	}
	snapshot, err := newMongoSnapshotStoreWithClient(ctx, client, dbName)
	if err != nil {
		_ = client.Disconnect(ctx)
		return nil, err
	}

	return &MongoStores{client: client, Movie: movie, ImageMap: imageMap, Snapshot: snapshot}, nil
}

// Close 断开共享的 MongoDB 连接。应在服务关闭时调用。
func (s *MongoStores) Close(ctx context.Context) error {
	if s == nil || s.client == nil {
		return nil
	}
	return s.client.Disconnect(ctx)
}
