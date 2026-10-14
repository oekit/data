// Package gormrepo 提供 data/crud 仓储接口的 GORM 实现。
//
// 根包 data/crud 保持 ORM 无关（接口 + 中性 Query 构建器），本包把
// QuerySpec 翻译为 gorm.DB 语句——import 根包不再传递编译依赖 GORM。
//
//	import (
//		"github.com/yi-nology/ekit-data/data/crud"
//		gormrepo "github.com/yi-nology/ekit-data/data/crud/gormrepo"
//	)
//
//	repo := gormrepo.NewGormRepository[Project](db)
//	svc := crud.NewBaseService[Project, CreateReq, UpdateReq](repo)
package gormrepo

import (
	"context"
	"errors"
	"fmt"

	"github.com/yi-nology/ekit-core/pkg/pagination"
	"github.com/yi-nology/ekit-data/data/crud"
	"github.com/yi-nology/ekit-data/data/gormx"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// ErrNotFound 透传 crud.ErrNotFound（哨兵定义在根包，Service 层依赖它判 404）。
var ErrNotFound = crud.ErrNotFound

// GormRepository 是基于 GORM 的仓储实现。
type GormRepository[T any] struct {
	db *gorm.DB
}

// NewGormRepository 创建 GORM 仓储实例。
func NewGormRepository[T any](db *gorm.DB) *GormRepository[T] {
	return &GormRepository[T]{db: db}
}

// Create 创建记录。
func (r *GormRepository[T]) Create(ctx context.Context, entity *T) error {
	return r.db.WithContext(ctx).Create(entity).Error
}

// BatchCreate 批量创建记录。
func (r *GormRepository[T]) BatchCreate(ctx context.Context, entities []*T, batchSize int) error {
	if batchSize <= 0 {
		batchSize = 100
	}
	return r.db.WithContext(ctx).CreateInBatches(entities, batchSize).Error
}

// RunInTx 在事务中执行 fn（TxRepository 能力）。
func (r *GormRepository[T]) RunInTx(ctx context.Context, fn func(repo crud.Repository[T]) error) error {
	return r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		return fn(NewGormRepository[T](tx))
	})
}

// GetByIDForUpdate 按 ID 行锁读取（TxRepository 能力）。
// sqlite 方言跳过 FOR UPDATE 子句：dialector 不支持该语法会直接报
// 语法错误，且 sqlite 单写者模型下事务天然串行，锁无增量价值。
func (r *GormRepository[T]) GetByIDForUpdate(ctx context.Context, id int64) (*T, error) {
	q := crud.NewQuery().Where("id", "=", id)
	if r.db.Name() != "sqlite" {
		q = q.ForUpdate()
	}
	return r.GetOne(ctx, q)
}

// GetByID 根据 ID 获取记录。
func (r *GormRepository[T]) GetByID(ctx context.Context, id int64) (*T, error) {
	var entity T
	err := r.db.WithContext(ctx).First(&entity, id).Error
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, fmt.Errorf("%w: id=%d", crud.ErrNotFound, id)
		}
		return nil, err
	}
	return &entity, nil
}

// GetOne 根据查询条件获取单条记录。
func (r *GormRepository[T]) GetOne(ctx context.Context, q *crud.Query) (*T, error) {
	var entity T
	db := r.db.WithContext(ctx)
	if q != nil {
		db = ApplyQuery(db, q)
	}
	err := db.First(&entity).Error
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, crud.ErrNotFound
		}
		return nil, err
	}
	return &entity, nil
}

// GetByCondition 根据原始 where 条件获取单条记录。
func (r *GormRepository[T]) GetByCondition(ctx context.Context, query any, args ...any) (*T, error) {
	var entity T
	err := r.db.WithContext(ctx).Where(query, args...).First(&entity).Error
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, crud.ErrNotFound
		}
		return nil, err
	}
	return &entity, nil
}

// List 查询列表并返回分页结果。
// total 在不带分页的条件下统计，分页仅作用于列表查询。
// 带 GroupBy 时 total 为分组数（对分组结果做子查询计数）——
// 直接 COUNT(*) 会按组聚合、GORM 只读第一行，total 退化为第一组的行数。
func (r *GormRepository[T]) List(ctx context.Context, q *crud.Query) ([]T, int64, error) {
	var total int64
	// Count 必须在不带 OFFSET/LIMIT 的语句上执行：
	// OFFSET 会跳过聚合行，page>=2 时 total 恒为 0
	if q != nil && q.Spec().GroupBy != "" {
		// GroupBy 语句：子查询对分组结果计数（total = 分组数，与列表语义一致）
		sub := countStatement(r.db.WithContext(ctx).Model(new(T)), q, true)
		if err := r.db.WithContext(ctx).Table("(?) AS grouped_count", sub).Count(&total).Error; err != nil {
			return nil, 0, err
		}
	} else {
		countDB := r.db.WithContext(ctx)
		if q != nil {
			countDB = countStatement(countDB, q, false)
		}
		if err := countDB.Model(new(T)).Count(&total).Error; err != nil {
			return nil, 0, err
		}
	}

	listDB := r.db.WithContext(ctx)
	if q != nil {
		listDB = ApplyQuery(listDB, q)
	}
	var list []T
	if err := listDB.Find(&list).Error; err != nil {
		return nil, 0, err
	}

	return list, total, nil
}

// Update 更新记录。
func (r *GormRepository[T]) Update(ctx context.Context, entity *T) error {
	return r.db.WithContext(ctx).Save(entity).Error
}

// UpdateFields 根据 ID 更新指定字段。
// 主键列由 GORM schema 解析（不要求主键列名为 "id"）。
func (r *GormRepository[T]) UpdateFields(ctx context.Context, id int64, fields map[string]any) error {
	var entity T
	return r.db.WithContext(ctx).Model(&entity).
		Where(clause.Eq{Column: clause.PrimaryColumn, Value: id}).
		Updates(fields).Error
}

// Delete 根据 ID 删除记录。
func (r *GormRepository[T]) Delete(ctx context.Context, id int64) error {
	var entity T
	return r.db.WithContext(ctx).Delete(&entity, id).Error
}

// BatchDelete 批量删除记录。
func (r *GormRepository[T]) BatchDelete(ctx context.Context, ids []int64) error {
	var entity T
	return r.db.WithContext(ctx).Delete(&entity, ids).Error
}

// Exists 检查记录是否存在。
// 分页条件不参与判断（存在性语义与页码无关）。
func (r *GormRepository[T]) Exists(ctx context.Context, q *crud.Query) (bool, error) {
	var count int64
	db := r.db.WithContext(ctx)
	if q != nil {
		// countStatement 剔除排序与 FOR UPDATE:聚合语句带锁子句在
		// PostgreSQL 直接报错、SQLite 语法错误,MySQL 无意义锁行
		db = countStatement(db, q, false)
	}
	if err := db.Model(new(T)).Count(&count).Error; err != nil {
		return false, err
	}
	return count > 0, nil
}

// Count 统计记录数。
// 分页条件不参与统计（计数语义与页码无关）。
func (r *GormRepository[T]) Count(ctx context.Context, q *crud.Query) (int64, error) {
	var count int64
	db := r.db.WithContext(ctx)
	if q != nil {
		// 同 Exists:计数语句不带排序/悲观锁/GroupBy(见 countStatement 注释)
		db = countStatement(db, q, false)
	}
	if err := db.Model(new(T)).Count(&count).Error; err != nil {
		return 0, err
	}
	return count, nil
}

// DB 获取底层 gorm.DB。
func (r *GormRepository[T]) DB() *gorm.DB {
	return r.db
}

// WithTx 返回绑定到指定事务的仓储副本。
// 返回具体类型以保留 DB() 等 GormRepository 特有方法。
func (r *GormRepository[T]) WithTx(tx *gorm.DB) *GormRepository[T] {
	return &GormRepository[T]{db: tx}
}

// PaginateList 使用 gormx.Paginate 执行分页查询。
func PaginateList[T any](db *gorm.DB, req *pagination.PageRequest, q *crud.Query) (*crud.PageResponse[T], error) {
	resp, err := gormx.Paginate[T](db, req, func(db *gorm.DB) *gorm.DB {
		if q != nil {
			return ApplyQuery(db, q)
		}
		return db
	})
	if err != nil {
		return nil, err
	}
	// gormx.PageResponse 已与 crud.PageResponse 统一为同一类型，直接返回
	return resp, nil
}
