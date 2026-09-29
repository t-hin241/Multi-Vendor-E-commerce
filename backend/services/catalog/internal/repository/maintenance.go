package repository

import (
 "context"
 "github.com/jackc/pgx/v5"
 "github.com/jackc/pgx/v5/pgxpool"
 "shopee/backend/pkg/apperror"
)
type Maintenance struct{Pool *pgxpool.Pool}
func(r Maintenance)Stats(ctx context.Context)(map[string]int64,error){
 result:=map[string]int64{}
 rows,err:=r.Pool.Query(ctx,`SELECT 'status_pending',count(*) FROM product_status_outbox UNION ALL
 SELECT 'status_parked',count(*) FROM product_status_outbox WHERE attempts>=10 UNION ALL
 SELECT 'cleanup_pending',count(*) FROM catalog_object_cleanup UNION ALL
 SELECT 'cleanup_parked',count(*) FROM catalog_object_cleanup WHERE attempts>=10 UNION ALL
 SELECT 'vendor_cache_stale',count(*) FROM vendor_name_cache WHERE updated_at<=now()-interval '15 minutes' UNION ALL
 SELECT 'sales_cache_stale',count(*) FROM product_sales_cache WHERE updated_at<=now()-interval '15 minutes' UNION ALL
 SELECT 'stock_cache_stale',count(*) FROM variant_stock_cache WHERE updated_at<=now()-interval '60 seconds'`)
 if err!=nil{return nil,err};defer rows.Close()
 for rows.Next(){var name string;var count int64;if err:=rows.Scan(&name,&count);err!=nil{return nil,err};result[name]=count}
 return result,rows.Err()
}
func(r Maintenance)Replay(ctx context.Context,actor,kind,id,reason string)error{
 return (Transactions{Pool:r.Pool}).Run(ctx,func(ctx context.Context)error{
  q:=connection(ctx,r.Pool);var product string;var err error
  if kind=="status"{err=q.QueryRow(ctx,`UPDATE product_status_outbox SET attempts=0,next_attempt_at=now(),last_error=NULL WHERE product_id::text=$1 RETURNING product_id`,id).Scan(&product)}else{err=q.QueryRow(ctx,`UPDATE catalog_object_cleanup SET attempts=0,next_attempt_at=greatest(created_at+interval '24 hours',now()),last_error=NULL WHERE object_key=$1 RETURNING product_id`,id).Scan(&product)}
  if err==pgx.ErrNoRows{return apperror.NotFound("Pending operation not found")};if err!=nil{return err}
  return NewAuditLogRepository(r.Pool).Create(ctx,product,actor,"replay_"+kind,&reason)
 })
}
