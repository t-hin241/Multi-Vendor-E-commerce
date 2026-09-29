package repository

import "context"

func(r *StorefrontCacheRepository) StaleIDs(ctx context.Context)(vendors,products,variants []string,err error){
 for _,item:=range []struct{query string;target *[]string}{
  {`SELECT vendor_id FROM vendor_name_cache WHERE updated_at<now()-interval '10 minutes' ORDER BY updated_at,vendor_id LIMIT 100`,&vendors},
  {`SELECT product_id FROM product_sales_cache WHERE updated_at<now()-interval '10 minutes' ORDER BY updated_at,product_id LIMIT 100`,&products},
  {`SELECT variant_id FROM variant_stock_cache WHERE updated_at<now()-interval '30 seconds' ORDER BY updated_at,variant_id LIMIT 100`,&variants},
 }{
  rows,e:=r.pool.Query(ctx,item.query);if e!=nil{err=e;return}
  for rows.Next(){var id string;if e=rows.Scan(&id);e!=nil{rows.Close();err=e;return};*item.target=append(*item.target,id)}
  rows.Close();if e=rows.Err();e!=nil{err=e;return}
 };return
}
