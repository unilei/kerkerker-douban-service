package repository

import (
	"context"
	"errors"
	"fmt"
	"time"

	"kerkerker-douban-service/internal/model"

	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo"
	"go.mongodb.org/mongo-driver/v2/mongo/options"
	"go.mongodb.org/mongo-driver/v2/mongo/readpref"
)

// RefreshStatus 描述一条影片数据的刷新状态。
type RefreshStatus string

const (
	RefreshStatusFresh RefreshStatus = "fresh" // 数据最新，无需回源
	RefreshStatusStale RefreshStatus = "stale" // 已过期，待刷新
	RefreshStatusError RefreshStatus = "error" // 上次刷新失败
)

// Movie 是 movies 集合的文档结构，作为 Redis 之后的持久真相源。
type Movie struct {
	InternalID    int64                `bson:"internal_id"`
	DoubanID      string               `bson:"douban_id"`
	TMDBID        int64                `bson:"tmdb_id,omitempty"`
	Title         string               `bson:"title"`
	Rate          string               `bson:"rate"`
	Cover         string               `bson:"cover"`
	URL           string               `bson:"url"`
	Detail        *model.SubjectDetail `bson:"detail,omitempty"`
	RefreshStatus RefreshStatus        `bson:"refresh_status"`
	LastRefreshed time.Time            `bson:"last_refreshed"`
	RefreshError  string               `bson:"refresh_error,omitempty"`
	CreatedAt     time.Time            `bson:"created_at"`
	UpdatedAt     time.Time            `bson:"updated_at"`
}

// ErrMovieNotFound 表示按 ID 查询时未命中。
var ErrMovieNotFound = errors.New("movie not found")

// MovieStore 提供对影片持久层的访问。Mongo 不可用时调用方应降级为纯 Redis 行为。
type MovieStore interface {
	GetByDoubanID(ctx context.Context, doubanID string) (*Movie, error)
	GetByInternalID(ctx context.Context, internalID int64) (*Movie, error)
	// Upsert 按 douban_id 落库：已存在则更新快照字段，新增则分配 internal_id 后插入。
	Upsert(ctx context.Context, m *Movie) error
	ListStale(ctx context.Context, limit int, refreshedBefore time.Time) ([]*Movie, error)
	MarkStale(ctx context.Context, refreshedBefore time.Time) (int64, error)
	Ping(ctx context.Context) error
	Close(ctx context.Context) error
}

// mongoMovieStore 是 MovieStore 的 MongoDB 实现。
type mongoMovieStore struct {
	client    *mongo.Client
	database  *mongo.Database
	movies    *mongo.Collection
	ownsClose bool // 是否由本 store 负责 Disconnect（独立构造时为 true，共享构造时为 false）
}

// NewMongoMovieStore 建立到 MongoDB 的连接并确保索引就绪。
func NewMongoMovieStore(ctx context.Context, uri, dbName string) (MovieStore, error) {
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

	store, err := newMongoMovieStoreWithClient(ctx, client, dbName)
	if err != nil {
		_ = client.Disconnect(ctx)
		return nil, err
	}
	store.ownsClose = true
	return store, nil
}

// newMongoMovieStoreWithClient 复用已建立的 *mongo.Client 构造 MovieStore（由 NewMongoStores 调用）。
func newMongoMovieStoreWithClient(ctx context.Context, client *mongo.Client, dbName string) (*mongoMovieStore, error) {
	db := client.Database(dbName)
	store := &mongoMovieStore{
		client:   client,
		database: db,
		movies:   db.Collection("movies"),
	}
	if err := store.ensureIndexes(ctx); err != nil {
		return nil, err
	}
	return store, nil
}

func (s *mongoMovieStore) ensureIndexes(ctx context.Context) error {
	idx := s.movies.Indexes()
	models := []mongo.IndexModel{
		{Keys: bson.D{{Key: "internal_id", Value: 1}}, Options: options.Index().SetUnique(true)},
		{Keys: bson.D{{Key: "douban_id", Value: 1}}, Options: options.Index().SetUnique(true)},
		{Keys: bson.D{{Key: "tmdb_id", Value: 1}}, Options: options.Index().SetSparse(true)},
		{Keys: bson.D{{Key: "refresh_status", Value: 1}, {Key: "last_refreshed", Value: 1}}},
	}
	if _, err := idx.CreateMany(ctx, models); err != nil {
		return fmt.Errorf("create indexes: %w", err)
	}
	return nil
}

func (s *mongoMovieStore) GetByDoubanID(ctx context.Context, doubanID string) (*Movie, error) {
	var m Movie
	err := s.movies.FindOne(ctx, bson.D{{Key: "douban_id", Value: doubanID}}).Decode(&m)
	if errors.Is(err, mongo.ErrNoDocuments) {
		return nil, ErrMovieNotFound
	}
	if err != nil {
		return nil, err
	}
	return &m, nil
}

func (s *mongoMovieStore) GetByInternalID(ctx context.Context, internalID int64) (*Movie, error) {
	var m Movie
	err := s.movies.FindOne(ctx, bson.D{{Key: "internal_id", Value: internalID}}).Decode(&m)
	if errors.Is(err, mongo.ErrNoDocuments) {
		return nil, ErrMovieNotFound
	}
	if err != nil {
		return nil, err
	}
	return &m, nil
}

func (s *mongoMovieStore) Upsert(ctx context.Context, m *Movie) error {
	if m.DoubanID == "" {
		return errors.New("upsert requires douban_id")
	}
	now := time.Now().UTC()

	var existing Movie
	err := s.movies.FindOne(ctx, bson.D{{Key: "douban_id", Value: m.DoubanID}}).Decode(&existing)
	switch {
	case err == nil:
		// 已存在：保留 internal_id，更新快照字段。
		s.applyUpdateFields(m, &existing, now)
		return s.updateOne(ctx, m)
	case errors.Is(err, mongo.ErrNoDocuments):
		// 新增：原子分配 internal_id。
		if m.InternalID == 0 {
			id, idErr := s.allocateID(ctx)
			if idErr != nil {
				return idErr
			}
			m.InternalID = id
		}
		s.applyInsertFields(m, now)
		if _, err := s.movies.InsertOne(ctx, m); err != nil {
			// 并发竞态：另一请求已先插入同一 douban_id。
			// 回退到更新路径，复用对方分配的 internal_id；
			// 本次分配的 ID 会被浪费一个（计数器无法回滚），但调用方请求仍能成功。
			if mongo.IsDuplicateKeyError(err) {
				return s.upsertAfterConflict(ctx, m, now)
			}
			return fmt.Errorf("insert movie: %w", err)
		}
		return nil
	default:
		return fmt.Errorf("lookup movie: %w", err)
	}
}

// applyUpdateFields 填充更新路径所需的字段：保留既有 internal_id 与 created_at，刷新时间戳。
func (s *mongoMovieStore) applyUpdateFields(m, existing *Movie, now time.Time) {
	m.InternalID = existing.InternalID
	m.CreatedAt = existing.CreatedAt
	if m.CreatedAt.IsZero() {
		m.CreatedAt = now
	}
	m.UpdatedAt = now
	if m.RefreshStatus == "" {
		m.RefreshStatus = RefreshStatusFresh
	}
	if m.LastRefreshed.IsZero() {
		m.LastRefreshed = now
	}
}

// applyInsertFields 填充插入路径所需的字段（internal_id 已由调用方分配）。
func (s *mongoMovieStore) applyInsertFields(m *Movie, now time.Time) {
	if m.CreatedAt.IsZero() {
		m.CreatedAt = now
	}
	m.UpdatedAt = now
	if m.RefreshStatus == "" {
		m.RefreshStatus = RefreshStatusFresh
	}
	if m.LastRefreshed.IsZero() {
		m.LastRefreshed = now
	}
}

// updateOne 按 douban_id 执行字段更新（m 字段需先经过 applyUpdateFields/applyInsertFields 处理）。
func (s *mongoMovieStore) updateOne(ctx context.Context, m *Movie) error {
	filter := bson.D{{Key: "douban_id", Value: m.DoubanID}}
	update := bson.D{
		{Key: "$set", Value: bson.D{
			{Key: "internal_id", Value: m.InternalID},
			{Key: "tmdb_id", Value: m.TMDBID},
			{Key: "title", Value: m.Title},
			{Key: "rate", Value: m.Rate},
			{Key: "cover", Value: m.Cover},
			{Key: "url", Value: m.URL},
			{Key: "detail", Value: m.Detail},
			{Key: "refresh_status", Value: m.RefreshStatus},
			{Key: "last_refreshed", Value: m.LastRefreshed},
			{Key: "refresh_error", Value: m.RefreshError},
			{Key: "updated_at", Value: m.UpdatedAt},
		}},
	}
	if _, err := s.movies.UpdateOne(ctx, filter, update); err != nil {
		return fmt.Errorf("update movie: %w", err)
	}
	return nil
}

// upsertAfterConflict 处理 InsertOne 唯一键冲突后的回退：重新查询已存在记录，
// 复用其 internal_id，走标准更新路径。
func (s *mongoMovieStore) upsertAfterConflict(ctx context.Context, m *Movie, now time.Time) error {
	var existing Movie
	if err := s.movies.FindOne(ctx, bson.D{{Key: "douban_id", Value: m.DoubanID}}).Decode(&existing); err != nil {
		return fmt.Errorf("re-lookup movie after duplicate-key: %w", err)
	}
	s.applyUpdateFields(m, &existing, now)
	return s.updateOne(ctx, m)
}

// allocateID 通过 counters 集合原子自增分配 internal_id。
func (s *mongoMovieStore) allocateID(ctx context.Context) (int64, error) {
	coll := s.database.Collection("counters")
	filter := bson.D{{Key: "_id", Value: "movies_seq"}}
	update := bson.D{{Key: "$inc", Value: bson.D{{Key: "seq", Value: int64(1)}}}}
	opts := options.FindOneAndUpdate().
		SetUpsert(true).
		SetReturnDocument(options.After)
	var doc struct {
		Seq int64 `bson:"seq"`
	}
	if err := coll.FindOneAndUpdate(ctx, filter, update, opts).Decode(&doc); err != nil {
		return 0, fmt.Errorf("allocate internal_id: %w", err)
	}
	return doc.Seq, nil
}

func (s *mongoMovieStore) ListStale(ctx context.Context, limit int, refreshedBefore time.Time) ([]*Movie, error) {
	if limit <= 0 {
		limit = 100
	}
	filter := bson.D{
		{Key: "$or", Value: bson.A{
			bson.D{{Key: "refresh_status", Value: RefreshStatusStale}},
			bson.D{{Key: "last_refreshed", Value: bson.D{{Key: "$lt", Value: refreshedBefore}}}},
		}},
	}
	cursor, err := s.movies.Find(ctx, filter, options.Find().SetLimit(int64(limit)))
	if err != nil {
		return nil, fmt.Errorf("list stale: %w", err)
	}
	defer cursor.Close(ctx)

	var results []*Movie
	if err := cursor.All(ctx, &results); err != nil {
		return nil, fmt.Errorf("decode stale: %w", err)
	}
	return results, nil
}

// MarkStale 把超过阈值的影片标记为 stale，供刷新任务批量处理。
func (s *mongoMovieStore) MarkStale(ctx context.Context, refreshedBefore time.Time) (int64, error) {
	filter := bson.D{
		{Key: "refresh_status", Value: RefreshStatusFresh},
		{Key: "last_refreshed", Value: bson.D{{Key: "$lt", Value: refreshedBefore}}},
	}
	update := bson.D{{Key: "$set", Value: bson.D{
		{Key: "refresh_status", Value: RefreshStatusStale},
		{Key: "updated_at", Value: time.Now().UTC()},
	}}}
	res, err := s.movies.UpdateMany(ctx, filter, update)
	if err != nil {
		return 0, fmt.Errorf("mark stale: %w", err)
	}
	return res.ModifiedCount, nil
}

func (s *mongoMovieStore) Ping(ctx context.Context) error {
	pingCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	return s.client.Ping(pingCtx, readpref.Primary())
}

func (s *mongoMovieStore) Close(ctx context.Context) error {
	if s != nil && s.ownsClose && s.client != nil {
		return s.client.Disconnect(ctx)
	}
	return nil
}
