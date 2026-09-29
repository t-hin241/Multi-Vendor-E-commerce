package usecase

import (
 "context"
 "time"
)

type CacheReconciliation interface {
 StaleIDs(context.Context)(vendors,products,variants []string,err error)
}

func(uc *ProductUseCase) ReconcileCache(ctx context.Context,queue CacheReconciliation){
 tick:=time.NewTicker(time.Minute);defer tick.Stop()
 for{
  batchCtx,cancel:=context.WithTimeout(ctx,20*time.Second)
  vendors,products,variants,err:=queue.StaleIDs(batchCtx)
  uc.observe(batchCtx,"cache_reconciliation",err)
  if err==nil{
   if len(vendors)>0{uc.resolveVendorNames(batchCtx,vendors)}
   if len(products)>0{uc.resolveQuantitySold(batchCtx,products)}
   if len(variants)>0{uc.resolveVariantStock(batchCtx,variants)}
  }
  cancel();select{case<-ctx.Done():return;case<-tick.C:}
 }
}
