package repository

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"time"

	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo"
	"go.mongodb.org/mongo-driver/v2/mongo/options"
)

// ImageMapping 是 image_mappings 集合的文档，记录原图 URL 到 R2 镜像地址的映射，
// 使进程重启后能复用已上传的 R2 对象，避免重复上传。
// _id 使用原图 URL 的 sha256，既能去重又不依赖 URL 作为主键（部分 URL 极长或含特殊字符）。
type ImageMapping struct {
	ID          string    `bson:"_id"` // sha256(originalURL)
	OriginalURL string    `bson:"original_url"`
	R2URL       string    `bson:"r2_url"`
	UploadedAt  time.Time `bson:"uploaded_at"`
}

// ErrImageMappingNotFound 表示某原图 URL 尚未同步到 R2。
var ErrImageMappingNotFound = errors.New("image mapping not found")

// ImageMapStore 提供原图 URL → R2 镜像地址的持久映射查询。
type ImageMapStore interface {
	Get(ctx context.Context, originalURL string) (string, error) // 返回 r2_url，未命中返回 ErrImageMappingNotFound
	Put(ctx context.Context, originalURL, r2URL string) error
	Close(ctx context.Context) error
}

type mongoImageMapStore struct {
	client *mongo.Client
	coll   *mongo.Collection
}

// NewMongoImageMapStore 连接到指定数据库的 image_mappings 集合。
// 复用已建立的 *mongo.Client 以避免多余连接。
func NewMongoImageMapStore(ctx context.Context, client *mongo.Client, dbName string) (ImageMapStore, error) {
	return newMongoImageMapStoreWithClient(ctx, client, dbName)
}

// newMongoImageMapStoreWithClient 由 NewMongoStores 调用，复用共享 client。
func newMongoImageMapStoreWithClient(ctx context.Context, client *mongo.Client, dbName string) (*mongoImageMapStore, error) {
	db := client.Database(dbName)
	coll := db.Collection("image_mappings")
	store := &mongoImageMapStore{client: client, coll: coll}
	if err := store.ensureIndexes(ctx); err != nil {
		return nil, err
	}
	return store, nil
}

func (s *mongoImageMapStore) ensureIndexes(ctx context.Context) error {
	return nil
}

func mappingID(originalURL string) string {
	sum := sha256.Sum256([]byte(originalURL))
	return hex.EncodeToString(sum[:])
}

func (s *mongoImageMapStore) Get(ctx context.Context, originalURL string) (string, error) {
	if originalURL == "" {
		return "", ErrImageMappingNotFound
	}
	var m ImageMapping
	err := s.coll.FindOne(ctx, bson.D{{Key: "_id", Value: mappingID(originalURL)}}).Decode(&m)
	if errors.Is(err, mongo.ErrNoDocuments) {
		return "", ErrImageMappingNotFound
	}
	if err != nil {
		return "", err
	}
	return m.R2URL, nil
}

func (s *mongoImageMapStore) Put(ctx context.Context, originalURL, r2URL string) error {
	if originalURL == "" || r2URL == "" {
		return errors.New("original_url and r2_url are required")
	}
	filter := bson.D{{Key: "_id", Value: mappingID(originalURL)}}
	update := bson.D{
		{Key: "$set", Value: bson.D{
			{Key: "original_url", Value: originalURL},
			{Key: "r2_url", Value: r2URL},
			{Key: "uploaded_at", Value: time.Now().UTC()},
		}},
	}
	opts := options.UpdateOne().SetUpsert(true)
	if _, err := s.coll.UpdateOne(ctx, filter, update, opts); err != nil {
		return fmt.Errorf("put image mapping: %w", err)
	}
	return nil
}

func (s *mongoImageMapStore) Close(ctx context.Context) error {
	return nil
}
