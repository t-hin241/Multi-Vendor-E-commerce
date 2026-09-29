package usecase

import (
 "context"
 "strings"
 "shopee/backend/pkg/apperror"
)

type MaintenanceRepository interface {
 Stats(context.Context)(map[string]int64,error)
 Replay(context.Context,string,string,string,string)error
}
type Maintenance struct{Repository MaintenanceRepository; Identity RoleVerifier}
func(m Maintenance) Stats(ctx context.Context,actor string)(map[string]int64,error){
 if err:=m.Identity.RequireRole(ctx,actor,"admin");err!=nil{return nil,err}
 return m.Repository.Stats(ctx)
}
func(m Maintenance) Replay(ctx context.Context,actor,kind,id,reason string)error{
 if err:=m.Identity.RequireRole(ctx,actor,"admin");err!=nil{return err}
 if (kind!="status"&&kind!="cleanup") || strings.TrimSpace(reason)=="" || len(reason)>500{return apperror.Validation("Replay requires a valid kind and reason (maximum 500 bytes)")}
 return m.Repository.Replay(ctx,actor,kind,id,reason)
}
