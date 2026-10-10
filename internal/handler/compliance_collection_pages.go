package handler

import (
	"context"
	"encoding/csv"
	"errors"
	"io"
	"strconv"
	"time"

	"github.com/alphabravocompany/astronomer-go/internal/db/sqlc"
	"github.com/google/uuid"
)

const complianceCollectionPage int32 = 200
const complianceCollectionMaximum int32 = 100000
const complianceCollectionTimeout = 20 * time.Second

var errComplianceCollectionIncomplete = errors.New("compliance collection is incomplete or exceeds the 100000-row section limit")

func complianceProjectBindings(ctx context.Context,q ProjectPolicyQuerier,project uuid.UUID) ([]sqlc.ProjectRoleBinding,error) {
	out:=[]sqlc.ProjectRoleBinding{}
	seen:=map[uuid.UUID]bool{}
	for offset:=int32(0);offset<=complianceCollectionMaximum;offset+=complianceCollectionPage {
		if err:=ctx.Err();err!=nil {return nil,err}
		rows,err:=q.ListProjectRoleBindingsByProject(ctx,sqlc.ListProjectRoleBindingsByProjectParams{ProjectID:project,Limit:complianceCollectionPage,Offset:offset})
		if err!=nil {return nil,err}
		if len(rows)>200 || len(out)+len(rows)>100000 {return nil,errComplianceCollectionIncomplete}
		for _,row:=range rows {
			if row.ProjectID!=project || seen[row.ID] {return nil,errComplianceCollectionIncomplete}
			seen[row.ID]=true
		}
		out=append(out,rows...)
		if len(rows)<200 {return out,nil}
	}
	return nil,errComplianceCollectionIncomplete
}

// WriteBackupDrillHistoryCSV streams the current DESC history order in bounded
// batches. Count/read/limit/output failures are section errors, not successful
// partial exports. It does not claim a transactionally consistent snapshot.
func WriteBackupDrillHistoryCSV(ctx context.Context,w io.Writer,q BackupDrillHistoryQuerier) (written int64,err error) {
	ctx,cancel:=context.WithTimeout(ctx,complianceCollectionTimeout)
	defer cancel()
	if err=ctx.Err();err!=nil {return 0,err}
	total,err:=q.CountBackupDrillResults(ctx)
	if err!=nil {return 0,err}
	if total<0 || total>int64(complianceCollectionMaximum) {return 0,errComplianceCollectionIncomplete}
	cw:=csv.NewWriter(w)
	defer func() {
		cw.Flush()
		if err==nil {err=cw.Error()}
	}()
	if err=cw.Write(backupDrillHistoryCSVHeader);err!=nil || total==0 {return 0,err}
	seen:=map[uuid.UUID]bool{}
	for offset:=int32(0);offset<=complianceCollectionMaximum;offset+=complianceCollectionPage {
		if err=ctx.Err();err!=nil {return written,err}
		rows,readErr:=q.ListBackupDrillResults(ctx,sqlc.ListBackupDrillResultsParams{Limit:complianceCollectionPage,Offset:offset})
		if readErr!=nil {return written,readErr}
		if len(rows)>200 {return written,errComplianceCollectionIncomplete}
		for _,r:=range rows {
			if written>=total || seen[r.ID] {return written,errComplianceCollectionIncomplete}
			seen[r.ID]=true
			finished:=""
			if r.FinishedAt.Valid {finished=r.FinishedAt.Time.UTC().Format(time.RFC3339Nano)}
			schemaVer:=""
			if r.SchemaVersion.Valid {schemaVer=strconv.Itoa(int(r.SchemaVersion.Int32))}
			if err=cw.Write([]string{r.ID.String(),r.StartedAt.UTC().Format(time.RFC3339Nano),finished,r.Status,schemaVer,r.BackupKey,r.ErrorMessage});err!=nil {return written,err}
			written++
		}
		if len(rows)<200 {
			if written!=total {return written,errComplianceCollectionIncomplete}
			return written,nil
		}
	}
	return written,errComplianceCollectionIncomplete
}
