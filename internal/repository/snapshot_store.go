package repository

import (
	"context"
	"errors"
	"fmt"
	"time"

	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo"
	"go.mongodb.org/mongo-driver/v2/mongo/options"
)

// Snapshot 是 snapshots 集合的文档，用于持久化任意列表/聚合型快照（category/hero/latest 等）。
// payload 用 bson.M / 任意 JSON 可序列化结构存储，由调用方解释其含义。
type Snapshot struct {
	Key       string    `bson:"_id"`     // 稳定逻辑键，例如 "douban:category:hot_movies:page1:limit20"
	Payload   any       `bson:"payload"` // 快照内容
	UpdatedAt time.Time `bson:"updated_at"`
}

// ErrSnapshotNotFound 表示某快照键尚不存在。
var ErrSnapshotNotFound = errors.New("snapshot not found")

// SnapshotStore 提供按 key 存取任意列表快照的持久化能力，作为列表型 handler 的 Mongo 兜底层。
type SnapshotStore interface {
	// Load 把 key 对应的 payload 反序列化进 dest。未命中返回 ErrSnapshotNotFound。
	Load(ctx context.Context, key string, dest any) error
	// Store 用 upsert 写入 key 的快照（覆盖旧 payload）。
	Store(ctx context.Context, key string, payload any) error
	// Delete 删除指定快照。键不存在时也视为成功。
	Delete(ctx context.Context, key string) error
}

type mongoSnapshotStore struct {
	coll *mongo.Collection
}

func newMongoSnapshotStoreWithClient(ctx context.Context, client *mongo.Client, dbName string) (*mongoSnapshotStore, error) {
	// _id 天然唯一，无需额外建索引。
	return &mongoSnapshotStore{coll: client.Database(dbName).Collection("snapshots")}, nil
}

func (s *mongoSnapshotStore) Load(ctx context.Context, key string, dest any) error {
	var doc bson.Raw
	if err := s.coll.FindOne(ctx, bson.D{{Key: "_id", Value: key}}).Decode(&doc); err != nil {
		if errors.Is(err, mongo.ErrNoDocuments) {
			return ErrSnapshotNotFound
		}
		return fmt.Errorf("load snapshot: %w", err)
	}
	// 取出 v 字段的原始值（数组/文档/标量皆可），再按调用方提供的目标类型解码。
	rawVal, err := doc.LookupErr("v")
	if err != nil {
		return fmt.Errorf("snapshot missing v field: %w", err)
	}
	if err := rawVal.Unmarshal(dest); err != nil {
		return fmt.Errorf("decode snapshot payload: %w", err)
	}
	return nil
}

func (s *mongoSnapshotStore) Store(ctx context.Context, key string, payload any) error {
	filter := bson.D{{Key: "_id", Value: key}}
	update := bson.D{
		{Key: "$set", Value: bson.D{
			{Key: "v", Value: payload},
			{Key: "updated_at", Value: time.Now().UTC()},
		}},
	}
	if _, err := s.coll.UpdateOne(ctx, filter, update, options.UpdateOne().SetUpsert(true)); err != nil {
		return fmt.Errorf("store snapshot: %w", err)
	}
	return nil
}

func (s *mongoSnapshotStore) Delete(ctx context.Context, key string) error {
	if _, err := s.coll.DeleteOne(ctx, bson.D{{Key: "_id", Value: key}}); err != nil {
		return fmt.Errorf("delete snapshot: %w", err)
	}
	return nil
}
