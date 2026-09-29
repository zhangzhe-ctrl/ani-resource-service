package data

import (
 "context"
 "crypto/sha256"
 "encoding/hex"
 "fmt"
 "io/fs"
 "sort"
 "time"

 "github.com/jackc/pgx/v5"
 "github.com/jackc/pgx/v5/pgxpool"
 imagemigrations "github.com/zhangzhe-ctrl/ani-resource-service/migrations/image"
)

const imageMigrationLock int64 = 84014001
var imageTables=[]string{"spaces","credentials","registrations","commands","schema_version"}

type OwnerConfig struct { DSN, RuntimeRole string }

type migration struct { Name, Checksum string; Body []byte; Version int32 }
func migrationFiles()([]migration,error) {
 names,err:=fs.Glob(imagemigrations.Files,"*.sql");if err!=nil{return nil,err};sort.Strings(names)
 result:=make([]migration,0,len(names))
 for i,name:=range names{body,err:=imagemigrations.Files.ReadFile(name);if err!=nil{return nil,err};hash:=sha256.Sum256(body);result=append(result,migration{Name:name,Body:body,Checksum:hex.EncodeToString(hash[:]),Version:int32(i+1)})}
 if len(result)==0{return nil,fmt.Errorf("Image migration stream is empty")};return result,nil
}

// ApplyImageMigrations is an owner-only explicit command. It does not alter
// database/PUBLIC privileges, unrelated schemas, or the Network version stream.
func ApplyImageMigrations(ctx context.Context, cfg OwnerConfig) error {
 if cfg.RuntimeRole==""{return fmt.Errorf("Image runtime role is required")}
 pool,err:=pgxpool.New(ctx,cfg.DSN);if err!=nil{return fmt.Errorf("invalid Image migration database configuration")};defer pool.Close()
 conn,err:=pool.Acquire(ctx);if err!=nil{return fmt.Errorf("Image migration database unavailable")};defer conn.Release()
 if _,err=conn.Exec(ctx,"SELECT pg_advisory_lock($1)",imageMigrationLock);err!=nil{return fmt.Errorf("Image migration lock unavailable")}
 defer func(){cleanup,cancel:=context.WithTimeout(context.Background(),5*time.Second);defer cancel();var unlocked bool
  if err:=conn.QueryRow(cleanup,"SELECT pg_advisory_unlock($1)",imageMigrationLock).Scan(&unlocked);err!=nil||!unlocked{_ = conn.Hijack().Close(cleanup)}
 }()
 var ownRole string;var targetExists bool
 if err=conn.QueryRow(ctx,"SELECT current_user,EXISTS(SELECT 1 FROM pg_roles WHERE rolname=$1)",cfg.RuntimeRole).Scan(&ownRole,&targetExists);err!=nil{return fmt.Errorf("Image role inspection failed")}
 if ownRole==cfg.RuntimeRole||!targetExists{return fmt.Errorf("distinct existing Image owner and runtime roles required")}
 var schemaPresent,owned bool
 if err=conn.QueryRow(ctx,"SELECT EXISTS(SELECT 1 FROM pg_namespace WHERE nspname='image'),EXISTS(SELECT 1 FROM pg_namespace WHERE nspname='image' AND nspowner=(SELECT oid FROM pg_roles WHERE rolname=current_user))").Scan(&schemaPresent,&owned);err!=nil{return fmt.Errorf("Image schema inspection failed")}
 if schemaPresent&&!owned{return fmt.Errorf("Image schema belongs to another owner")}
 var present bool
 if err=conn.QueryRow(ctx,"SELECT to_regclass('image.schema_version') IS NOT NULL").Scan(&present);err!=nil{return fmt.Errorf("Image version inspection failed")}
 if !present&&schemaPresent{var count int;if err=conn.QueryRow(ctx,"SELECT count(*) FROM pg_class c JOIN pg_namespace n ON n.oid=c.relnamespace WHERE n.nspname='image'").Scan(&count);err!=nil{return fmt.Errorf("Image schema inspection failed")};if count!=0{return fmt.Errorf("unversioned Image schema has existing objects")}}
 files,err:=migrationFiles();if err!=nil{return err}
 if present{var count int;var max int32;if err=conn.QueryRow(ctx,"SELECT count(*),COALESCE(max(version),0) FROM image.schema_version").Scan(&count,&max);err!=nil{return fmt.Errorf("Image version inspection failed")};if count!=int(max)||count>len(files){return fmt.Errorf("unsupported Image migration sequence")}}
 for _,file:=range files {
  if present{var checksum string;err=conn.QueryRow(ctx,"SELECT checksum FROM image.schema_version WHERE version=$1",file.Version).Scan(&checksum)
   if err==nil{if checksum!=file.Checksum{return fmt.Errorf("Image migration %s checksum mismatch",file.Name)};continue}
   if err!=pgx.ErrNoRows{return fmt.Errorf("Image migration version read failed")}
  }
  tx,err:=conn.Begin(ctx);if err!=nil{return fmt.Errorf("Image migration transaction unavailable")}
  if _,err=tx.Exec(ctx,string(file.Body));err==nil{_,err=tx.Exec(ctx,"INSERT INTO image.schema_version(version,checksum) VALUES($1,$2)",file.Version,file.Checksum)}
  if err!=nil{_ = tx.Rollback(ctx);return fmt.Errorf("Image migration %s failed",file.Name)}
  if err=tx.Commit(ctx);err!=nil{return fmt.Errorf("Image migration commit failed")};present=true
 }
 // Identifier is deployment configuration, quoted as an identifier, never SQL.
 role:=pgx.Identifier{cfg.RuntimeRole}.Sanitize()
 tx,err:=conn.Begin(ctx);if err!=nil{return fmt.Errorf("Image grant transaction unavailable")};defer tx.Rollback(ctx)
 if _,err=tx.Exec(ctx,"REVOKE ALL ON SCHEMA image FROM PUBLIC; REVOKE CREATE ON SCHEMA image FROM "+role+"; GRANT USAGE ON SCHEMA image TO "+role);err!=nil{return fmt.Errorf("Image schema grants failed")}
 for _,table:=range imageTables {
  permissions:="SELECT, INSERT, UPDATE";if table=="schema_version"{permissions="SELECT"}
  name:=pgx.Identifier{"image",table}.Sanitize()
  if _,err=tx.Exec(ctx,"REVOKE ALL ON "+name+" FROM PUBLIC; REVOKE ALL ON "+name+" FROM "+role+"; GRANT "+permissions+" ON "+name+" TO "+role);err!=nil{return fmt.Errorf("Image table grants failed")}
 }
 if err=tx.Commit(ctx);err!=nil{return fmt.Errorf("Image grant commit failed")};return nil
}

type rowQuerier interface { QueryRow(context.Context,string,...any) pgx.Row }
func VerifyImageMigrationChecksums(ctx context.Context,q rowQuerier)error {
 files,err:=migrationFiles();if err!=nil{return err}
 var count int
 if err=q.QueryRow(ctx,"SELECT count(*) FROM image.schema_version").Scan(&count);err!=nil{return fmt.Errorf("Image schema version unavailable")}
 if count!=len(files){return fmt.Errorf("unsupported Image schema version")}
 for _,file:=range files{var checksum string;if err=q.QueryRow(ctx,"SELECT checksum FROM image.schema_version WHERE version=$1",file.Version).Scan(&checksum);err!=nil{return fmt.Errorf("Image migration version unavailable")};if checksum!=file.Checksum{return fmt.Errorf("Image migration %s checksum mismatch",file.Name)}}
 return nil
}
