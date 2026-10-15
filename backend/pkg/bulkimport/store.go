package bulkimport

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"strconv"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

// Querier is the owner's connection: its transaction when ctx carries one.
type Querier interface {
	Exec(context.Context, string, ...any) (pgconn.CommandTag, error)
	Query(context.Context, string, ...any) (pgx.Rows, error)
	QueryRow(context.Context, string, ...any) pgx.Row
}

// Store keeps jobs in the owner's database. Conn and Transact come from the
// owner's repository so a row result commits with the owner's mutation.
type Store struct {
	Conn     func(context.Context) Querier
	Transact func(context.Context, func(context.Context) error) error
}

var (
	errNotFound     = errors.New("bulkimport: not found")
	errDuplicateKey = errors.New("bulkimport: duplicate idempotency key")
	errLeaseLost    = errors.New("bulkimport: row lease lost")
)

const jobColumns = `j.id,j.vendor_id,j.kind,j.template_version,j.status,j.file_name,j.file_hash,j.file_bytes,j.header,j.preview_hash,j.preview_at,
 coalesce(j.duplicate_of::text,''),coalesce(j.error_code,''),coalesce(j.error_message,''),j.version,j.requested_by,coalesce(j.confirmed_by::text,''),
 j.created_at,j.updated_at,j.finished_at,j.object_key,j.idempotency_key,j.attempts`

const summaryColumns = `,(SELECT count(*) FROM import_rows r WHERE r.job_id=j.id),
 (SELECT count(*) FROM import_rows r WHERE r.job_id=j.id AND r.status='invalid'),
 (SELECT count(*) FROM import_rows r WHERE r.job_id=j.id AND r.status='unchanged'),
 (SELECT count(*) FROM import_rows r WHERE r.job_id=j.id AND r.status IN ('valid','applying')),
 (SELECT count(*) FROM import_rows r WHERE r.job_id=j.id AND r.status='succeeded'),
 (SELECT count(*) FROM import_rows r WHERE r.job_id=j.id AND r.status='needs_approval'),
 (SELECT count(*) FROM import_rows r WHERE r.job_id=j.id AND r.status='failed'),
 (SELECT count(*) FROM import_rows r WHERE r.job_id=j.id AND r.status='cancelled')`

type scanner interface{ Scan(...any) error }

func scanJob(row scanner, summary bool) (*Job, error) {
	var j Job
	var header []byte
	dest := []any{&j.ID, &j.VendorID, &j.Kind, &j.TemplateVersion, &j.Status, &j.FileName, &j.FileHash, &j.FileBytes, &header, &j.PreviewHash, &j.PreviewAt,
		&j.DuplicateOf, &j.ErrorCode, &j.ErrorMessage, &j.Version, &j.RequestedBy, &j.ConfirmedBy,
		&j.CreatedAt, &j.UpdatedAt, &j.FinishedAt, &j.objectKey, &j.idempotencyKey, &j.attempts}
	if summary {
		s := &j.Summary
		dest = append(dest, &s.Total, &s.Invalid, &s.Unchanged, &s.Pending, &s.Succeeded, &s.NeedsApproval, &s.Failed, &s.Cancelled)
	}
	if err := row.Scan(dest...); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, errNotFound
		}
		return nil, err
	}
	if err := json.Unmarshal(header, &j.Header); err != nil {
		return nil, err
	}
	return &j, nil
}

func (s Store) JobByKey(ctx context.Context, vendor, key string) (*Job, error) {
	return scanJob(s.Conn(ctx).QueryRow(ctx, `SELECT `+jobColumns+summaryColumns+` FROM import_jobs j WHERE j.vendor_id=$1 AND j.idempotency_key=$2`, vendor, key), true)
}

func (s Store) Job(ctx context.Context, id string) (*Job, error) {
	return scanJob(s.Conn(ctx).QueryRow(ctx, `SELECT `+jobColumns+summaryColumns+` FROM import_jobs j WHERE j.id=$1`, id), true)
}

// LockJob reads a job for update inside the caller's transaction.
func (s Store) LockJob(ctx context.Context, id string) (*Job, error) {
	j, err := scanJob(s.Conn(ctx).QueryRow(ctx, `SELECT `+jobColumns+` FROM import_jobs j WHERE j.id=$1 FOR UPDATE`, id), false)
	if err != nil {
		return nil, err
	}
	j.Summary, err = s.summary(ctx, id)
	return j, err
}

func (s Store) summary(ctx context.Context, id string) (Summary, error) {
	var out Summary
	rows, err := s.Conn(ctx).Query(ctx, `SELECT status,count(*) FROM import_rows WHERE job_id=$1 GROUP BY status`, id)
	if err != nil {
		return out, err
	}
	defer rows.Close()
	for rows.Next() {
		var status string
		var n int
		if err = rows.Scan(&status, &n); err != nil {
			return out, err
		}
		out.Total += n
		switch status {
		case RowInvalid:
			out.Invalid = n
		case RowUnchanged:
			out.Unchanged = n
		case RowValid, RowApplying:
			out.Pending += n
		case RowSucceeded:
			out.Succeeded = n
		case RowNeedsApproval:
			out.NeedsApproval = n
		case RowFailed:
			out.Failed = n
		case RowCancelled:
			out.Cancelled = n
		}
	}
	return out, rows.Err()
}

func (s Store) CreateJob(ctx context.Context, j *Job) error {
	header, err := json.Marshal(j.Header)
	if err != nil {
		return err
	}
	err = s.Conn(ctx).QueryRow(ctx, `INSERT INTO import_jobs(id,vendor_id,kind,template_version,requested_by,idempotency_key,file_name,file_hash,file_bytes,object_key,header)
 VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11) RETURNING status,version,created_at,updated_at`,
		j.ID, j.VendorID, j.Kind, j.TemplateVersion, j.RequestedBy, j.idempotencyKey, j.FileName, j.FileHash, j.FileBytes, j.objectKey, header).
		Scan(&j.Status, &j.Version, &j.CreatedAt, &j.UpdatedAt)
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) && pgErr.Code == "23505" {
		return errDuplicateKey
	}
	return err
}

type jobCursor struct {
	At time.Time `json:"at"`
	ID string    `json:"id"`
}

func parseJobCursor(raw string) (jobCursor, error) {
	if raw == "" {
		return jobCursor{At: time.Date(9999, 1, 1, 0, 0, 0, 0, time.UTC), ID: "ffffffff-ffff-ffff-ffff-ffffffffffff"}, nil
	}
	var c jobCursor
	b, err := base64.RawURLEncoding.DecodeString(raw)
	if err != nil || json.Unmarshal(b, &c) != nil || c.At.IsZero() {
		return c, errInvalidCursor
	}
	if _, err := uuid.Parse(c.ID); err != nil {
		return c, errInvalidCursor
	}
	return c, nil
}

func jobCursorOf(j Job) string {
	b, _ := json.Marshal(jobCursor{At: j.CreatedAt, ID: j.ID})
	return base64.RawURLEncoding.EncodeToString(b)
}

// Jobs lists jobs newest first; an empty vendor lists every shop (admin).
func (s Store) Jobs(ctx context.Context, vendor, kind, status, cursor string, limit int) (Page[Job], error) {
	out := Page[Job]{Items: []Job{}}
	c, err := parseJobCursor(cursor)
	if err != nil {
		return out, err
	}
	rows, err := s.Conn(ctx).Query(ctx, `SELECT `+jobColumns+summaryColumns+` FROM import_jobs j
 WHERE ($1='' OR j.vendor_id::text=$1) AND ($2='' OR j.status=$2) AND ($6='' OR j.kind=$6) AND (j.created_at,j.id)<($3,$4::uuid)
 ORDER BY j.created_at DESC,j.id DESC LIMIT $5`, vendor, status, c.At, c.ID, limit+1, kind)
	if err != nil {
		return out, err
	}
	defer rows.Close()
	for rows.Next() {
		j, err := scanJob(rows, true)
		if err != nil {
			return out, err
		}
		out.Items = append(out.Items, *j)
	}
	if err = rows.Err(); err != nil {
		return out, err
	}
	if len(out.Items) > limit {
		out.Items = out.Items[:limit]
		out.NextCursor = jobCursorOf(out.Items[limit-1])
	}
	return out, nil
}

// ClaimValidation leases the next uploaded job for its dry-run.
func (s Store) ClaimValidation(ctx context.Context) (*Job, error) {
	return scanJob(s.Conn(ctx).QueryRow(ctx, `UPDATE import_jobs j SET status='validating',attempts=j.attempts+1,next_attempt_at=now()+interval '3 minutes',updated_at=now()
 WHERE j.id=(SELECT id FROM import_jobs WHERE status IN ('uploaded','validating') AND next_attempt_at<=now() ORDER BY next_attempt_at,id LIMIT 1 FOR UPDATE SKIP LOCKED)
 RETURNING `+jobColumns), false)
}

type plannedRow struct {
	RowNo           int             `json:"row_no"`
	TargetKey       string          `json:"target_key"`
	Raw             []string        `json:"raw"`
	Payload         json.RawMessage `json:"payload"`
	PayloadHash     string          `json:"payload_hash"`
	ExpectedVersion *int64          `json:"expected_version"`
	Old             map[string]any  `json:"old_values"`
	New             map[string]any  `json:"new_values"`
	Status          string          `json:"status"`
	OperationRef    string          `json:"operation_ref"`
	ErrorCode       *string         `json:"error_code"`
	ErrorField      *string         `json:"error_field"`
	ErrorMessage    *string         `json:"error_message"`
}

// SavePreview stores the dry-run atomically, only if the validation lease
// is still ours.
func (s Store) SavePreview(ctx context.Context, j *Job, header []string, rows []plannedRow, previewHash, duplicateOf string) error {
	return s.Transact(ctx, func(ctx context.Context) error {
		q := s.Conn(ctx)
		var status string
		var attempts int
		if err := q.QueryRow(ctx, `SELECT status,attempts FROM import_jobs WHERE id=$1 FOR UPDATE`, j.ID).Scan(&status, &attempts); err != nil {
			return err
		}
		if status != StatusValidating || attempts != j.attempts {
			return errLeaseLost
		}
		if _, err := q.Exec(ctx, `DELETE FROM import_rows WHERE job_id=$1`, j.ID); err != nil {
			return err
		}
		data, err := json.Marshal(rows)
		if err != nil {
			return err
		}
		if _, err = q.Exec(ctx, `INSERT INTO import_rows(job_id,row_no,target_key,raw,payload,payload_hash,expected_version,old_values,new_values,status,operation_ref,error_code,error_field,error_message)
 SELECT $1,x.row_no,x.target_key,x.raw,x.payload,x.payload_hash,x.expected_version,x.old_values,x.new_values,x.status,x.operation_ref,x.error_code,x.error_field,x.error_message
 FROM jsonb_to_recordset($2::jsonb) AS x(row_no int,target_key text,raw jsonb,payload jsonb,payload_hash text,expected_version bigint,old_values jsonb,new_values jsonb,status text,operation_ref text,error_code text,error_field text,error_message text)`, j.ID, data); err != nil {
			return err
		}
		h, err := json.Marshal(header)
		if err != nil {
			return err
		}
		_, err = q.Exec(ctx, `UPDATE import_jobs SET status='preview_ready',header=$2,preview_hash=$3,preview_at=now(),duplicate_of=NULLIF($4,'')::uuid,
 version=version+1,updated_at=now(),file_expires_at=now()+make_interval(secs=>$5) WHERE id=$1`, j.ID, h, previewHash, duplicateOf, FileRetention.Seconds())
		return err
	})
}

// FailValidation ends the dry-run (terminal) or schedules a retry.
func (s Store) FailValidation(ctx context.Context, j *Job, status, code, message string, retryIn time.Duration) error {
	if status == "" {
		_, err := s.Conn(ctx).Exec(ctx, `UPDATE import_jobs SET status='uploaded',next_attempt_at=now()+make_interval(secs=>$3),updated_at=now() WHERE id=$1 AND attempts=$2 AND status='validating'`, j.ID, j.attempts, retryIn.Seconds())
		return err
	}
	_, err := s.Conn(ctx).Exec(ctx, `UPDATE import_jobs SET status=$3,error_code=$4,error_message=$5,version=version+1,updated_at=now(),finished_at=now(),file_expires_at=now()+make_interval(secs=>$6)
 WHERE id=$1 AND attempts=$2 AND status='validating'`, j.ID, j.attempts, status, code, message, FileRetention.Seconds())
	return err
}

// DuplicateFile finds an earlier job that already applied the same file
// with the same intent (kind) in this shop.
func (s Store) DuplicateFile(ctx context.Context, vendor, kind, hash, except string) (string, error) {
	var id string
	err := s.Conn(ctx).QueryRow(ctx, `SELECT id FROM import_jobs WHERE vendor_id=$1 AND kind=$2 AND file_hash=$3 AND id<>$4 AND confirmed_at IS NOT NULL
 ORDER BY created_at DESC LIMIT 1`, vendor, kind, hash, except).Scan(&id)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", nil
	}
	return id, err
}

func (s Store) MarkConfirmed(ctx context.Context, id, actor string) error {
	_, err := s.Conn(ctx).Exec(ctx, `UPDATE import_jobs SET status='confirmed',confirmed_by=$2,confirmed_at=now(),version=version+1,updated_at=now() WHERE id=$1`, id, actor)
	return err
}

// Cancel stops rows that were not applied yet; rows in flight finish (the
// job is cancelling until they do). Applied rows are never undone.
func (s Store) Cancel(ctx context.Context, id, actor, code, message string) error {
	q := s.Conn(ctx)
	if _, err := q.Exec(ctx, `UPDATE import_rows SET status='cancelled',error_code=$2,error_message=$3,updated_at=now() WHERE job_id=$1 AND status='valid'`, id, code, message); err != nil {
		return err
	}
	_, err := q.Exec(ctx, `UPDATE import_jobs SET status=CASE WHEN EXISTS(SELECT 1 FROM import_rows WHERE job_id=$1 AND status='applying') THEN 'cancelling' ELSE 'cancelled' END,
 cancelled_by=NULLIF($2,'')::uuid,version=version+1,updated_at=now(),finished_at=CASE WHEN EXISTS(SELECT 1 FROM import_rows WHERE job_id=$1 AND status='applying') THEN NULL ELSE now() END,
 file_expires_at=coalesce(file_expires_at,now()+make_interval(secs=>$3)) WHERE id=$1`, id, actor, FileRetention.Seconds())
	return err
}

// StopJob cancels the job's remaining rows after the actor lost the right
// to apply them (permission revoked, feature disabled).
func (s Store) StopJob(ctx context.Context, id, code, message string) error {
	q := s.Conn(ctx)
	if _, err := q.Exec(ctx, `UPDATE import_rows SET status='cancelled',error_code=$2,error_message=$3,updated_at=now() WHERE job_id=$1 AND status='valid'`, id, code, message); err != nil {
		return err
	}
	_, err := q.Exec(ctx, `UPDATE import_jobs SET error_code=$2,error_message=$3,version=version+1,updated_at=now() WHERE id=$1`, id, code, message)
	return err
}

// rowColumns reads import_rows aliased r.
const rowColumns = `r.job_id,r.row_no,r.status,r.target_key,r.raw,coalesce(r.payload,'null'::jsonb),r.expected_version,r.old_values,r.new_values,r.operation_ref,
 coalesce(r.result_ref,''),coalesce(r.error_code,''),coalesce(r.error_field,''),coalesce(r.error_message,''),r.attempts,r.updated_at`

func scanRow(row scanner) (*Row, error) {
	var r Row
	var raw, oldV, newV []byte
	var code, field, message string
	if err := row.Scan(&r.JobID, &r.RowNo, &r.Status, &r.TargetKey, &raw, &r.Payload, &r.ExpectedVersion, &oldV, &newV, &r.OperationRef, &r.ResultRef,
		&code, &field, &message, &r.Attempts, &r.UpdatedAt); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, errNotFound
		}
		return nil, err
	}
	if err := json.Unmarshal(raw, &r.Values); err != nil {
		return nil, err
	}
	for _, v := range []struct {
		b   []byte
		dst *map[string]any
	}{{oldV, &r.Old}, {newV, &r.New}} {
		if len(v.b) > 0 {
			if err := json.Unmarshal(v.b, v.dst); err != nil {
				return nil, err
			}
		}
	}
	if code != "" {
		r.Error = &RowError{Code: code, Field: field, Message: message}
	}
	return &r, nil
}

func (s Store) Rows(ctx context.Context, job, status, cursor string, limit int) (Page[Row], error) {
	out := Page[Row]{Items: []Row{}}
	after := 0
	if cursor != "" {
		b, err := base64.RawURLEncoding.DecodeString(cursor)
		if err != nil {
			return out, errInvalidCursor
		}
		if after, err = strconv.Atoi(string(b)); err != nil || after < 1 {
			return out, errInvalidCursor
		}
	}
	rows, err := s.Conn(ctx).Query(ctx, `SELECT `+rowColumns+` FROM import_rows r WHERE r.job_id=$1 AND ($2='' OR r.status=$2 OR ($2='pending' AND r.status IN ('valid','applying')))
 AND r.row_no>$3 ORDER BY r.row_no LIMIT $4`, job, status, after, limit+1)
	if err != nil {
		return out, err
	}
	defer rows.Close()
	for rows.Next() {
		r, err := scanRow(rows)
		if err != nil {
			return out, err
		}
		out.Items = append(out.Items, *r)
	}
	if err = rows.Err(); err != nil {
		return out, err
	}
	if len(out.Items) > limit {
		out.Items = out.Items[:limit]
		out.NextCursor = base64.RawURLEncoding.EncodeToString([]byte(strconv.Itoa(out.Items[limit-1].RowNo)))
	}
	return out, nil
}

// ProblemRows returns every row the vendor has to fix, in file order.
func (s Store) ProblemRows(ctx context.Context, job string) ([]Row, error) {
	rows, err := s.Conn(ctx).Query(ctx, `SELECT `+rowColumns+` FROM import_rows r WHERE r.job_id=$1 AND r.status IN ('invalid','failed','cancelled') ORDER BY r.row_no`, job)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Row
	for rows.Next() {
		r, err := scanRow(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *r)
	}
	return out, rows.Err()
}

type claimedRow struct {
	Row
	VendorID string
	Kind     string
	Actor    string
}

// ClaimRow leases the next row of a confirmed job (or a row whose lease
// expired after a crash; applying it again replays the same operation).
func (s Store) ClaimRow(ctx context.Context) (*claimedRow, error) {
	q := s.Conn(ctx)
	r, err := scanRow(q.QueryRow(ctx, `UPDATE import_rows r SET status='applying',attempts=r.attempts+1,lease_until=now()+interval '2 minutes',updated_at=now()
 FROM (SELECT x.job_id,x.row_no FROM import_rows x JOIN import_jobs j ON j.id=x.job_id
       WHERE (x.status='valid' AND j.status IN ('confirmed','applying')) OR (x.status='applying' AND x.lease_until<now())
       ORDER BY x.lease_until NULLS FIRST,j.confirmed_at,x.row_no LIMIT 1 FOR UPDATE OF x SKIP LOCKED) c
 WHERE r.job_id=c.job_id AND r.row_no=c.row_no RETURNING `+rowColumns))
	if err != nil {
		return nil, err
	}
	c := &claimedRow{Row: *r}
	err = q.QueryRow(ctx, `UPDATE import_jobs SET status=CASE WHEN status='confirmed' THEN 'applying' ELSE status END,updated_at=now() WHERE id=$1
 RETURNING vendor_id,kind,confirmed_by`, r.JobID).Scan(&c.VendorID, &c.Kind, &c.Actor)
	return c, err
}

// LockRow confirms, inside the apply transaction, that the lease is still
// ours; a second worker holding the same row then waits and loses.
func (s Store) LockRow(ctx context.Context, c *claimedRow) error {
	var status string
	var attempts int
	err := s.Conn(ctx).QueryRow(ctx, `SELECT status,attempts FROM import_rows WHERE job_id=$1 AND row_no=$2 FOR UPDATE`, c.JobID, c.RowNo).Scan(&status, &attempts)
	if err != nil {
		return err
	}
	if status != RowApplying || attempts != c.Attempts {
		return errLeaseLost
	}
	return nil
}

// FinishRow records a row's final result if the lease is still ours.
func (s Store) FinishRow(ctx context.Context, c *claimedRow, status, result string, e *RowError) error {
	var code, field, message *string
	if e != nil {
		code, field, message = &e.Code, &e.Field, &e.Message
	}
	_, err := s.Conn(ctx).Exec(ctx, `UPDATE import_rows SET status=$4,result_ref=NULLIF($5,''),error_code=$6,error_field=NULLIF($7,''),error_message=$8,lease_until=NULL,updated_at=now()
 WHERE job_id=$1 AND row_no=$2 AND attempts=$3 AND status='applying'`, c.JobID, c.RowNo, c.Attempts, status, result, code, field, message)
	return err
}

// RetryRow keeps the row leased until its backoff ends.
func (s Store) RetryRow(ctx context.Context, c *claimedRow, after time.Duration) error {
	_, err := s.Conn(ctx).Exec(ctx, `UPDATE import_rows SET lease_until=now()+make_interval(secs=>$4),updated_at=now() WHERE job_id=$1 AND row_no=$2 AND attempts=$3 AND status='applying'`,
		c.JobID, c.RowNo, c.Attempts, after.Seconds())
	return err
}

// FinalizeJob closes a job once no row is waiting or in flight.
func (s Store) FinalizeJob(ctx context.Context, id string) error {
	_, err := s.Conn(ctx).Exec(ctx, `UPDATE import_jobs SET status=CASE WHEN status='cancelling' THEN 'cancelled' ELSE 'completed' END,finished_at=now(),version=version+1,updated_at=now(),
 file_expires_at=now()+make_interval(secs=>$2)
 WHERE id=$1 AND status IN ('confirmed','applying','cancelling') AND NOT EXISTS(SELECT 1 FROM import_rows WHERE job_id=$1 AND status IN ('valid','applying'))`, id, FileRetention.Seconds())
	return err
}

func (s Store) Event(ctx context.Context, job, vendor, actor, action, reason, requestID string, detail map[string]any) error {
	if detail == nil {
		detail = map[string]any{}
	}
	b, err := json.Marshal(detail)
	if err != nil {
		return err
	}
	_, err = s.Conn(ctx).Exec(ctx, `INSERT INTO import_job_events(job_id,vendor_id,actor_id,action,reason,detail,request_id) VALUES($1,$2,NULLIF($3,'')::uuid,$4,NULLIF($5,''),$6,NULLIF($7,''))`,
		job, vendor, actor, action, reason, b, requestID)
	return err
}

// Report returns the stored error report when it still matches the job.
func (s Store) Report(ctx context.Context, id string, version int64) (string, error) {
	var key string
	err := s.Conn(ctx).QueryRow(ctx, `SELECT report_key FROM import_jobs WHERE id=$1 AND report_version=$2 AND report_expires_at>now()+interval '1 minute'`, id, version).Scan(&key)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", nil
	}
	return key, err
}

func (s Store) SaveReport(ctx context.Context, id, key string, version int64) (string, error) {
	var old string
	err := s.Conn(ctx).QueryRow(ctx, `UPDATE import_jobs j SET report_key=$2,report_version=$3,report_expires_at=now()+make_interval(secs=>$4) FROM import_jobs o
 WHERE j.id=$1 AND o.id=j.id RETURNING coalesce(o.report_key,'')`, id, key, version, ReportRetention.Seconds()).Scan(&old)
	return old, err
}

type storedFile struct{ ID, Key string }

// ExpiredFiles lists originals and reports past retention.
func (s Store) ExpiredFiles(ctx context.Context, limit int) ([]storedFile, []storedFile, error) {
	var files, reports []storedFile
	for _, q := range []struct {
		sql string
		out *[]storedFile
	}{
		{`SELECT id,object_key FROM import_jobs WHERE file_deleted_at IS NULL AND file_expires_at<now() ORDER BY file_expires_at LIMIT $1`, &files},
		{`SELECT id,report_key FROM import_jobs WHERE report_key IS NOT NULL AND report_expires_at<now() ORDER BY report_expires_at LIMIT $1`, &reports},
	} {
		rows, err := s.Conn(ctx).Query(ctx, q.sql, limit)
		if err != nil {
			return nil, nil, err
		}
		for rows.Next() {
			var f storedFile
			if err = rows.Scan(&f.ID, &f.Key); err != nil {
				rows.Close()
				return nil, nil, err
			}
			*q.out = append(*q.out, f)
		}
		rows.Close()
		if err = rows.Err(); err != nil {
			return nil, nil, err
		}
	}
	return files, reports, nil
}

func (s Store) MarkFileDeleted(ctx context.Context, id string) error {
	_, err := s.Conn(ctx).Exec(ctx, `UPDATE import_jobs SET file_deleted_at=now() WHERE id=$1`, id)
	return err
}

func (s Store) ClearReport(ctx context.Context, id, key string) error {
	_, err := s.Conn(ctx).Exec(ctx, `UPDATE import_jobs SET report_key=NULL,report_version=NULL,report_expires_at=NULL WHERE id=$1 AND report_key=$2`, id, key)
	return err
}

const exportColumns = `id,vendor_id,kind,status,row_count,requested_by,expires_at,created_at,object_key,idempotency_key,attempts`

func scanExport(row scanner) (*Export, error) {
	var e Export
	if err := row.Scan(&e.ID, &e.VendorID, &e.Kind, &e.Status, &e.RowCount, &e.RequestedBy, &e.ExpiresAt, &e.CreatedAt, &e.objectKey, &e.idempotencyKey, &e.attempts); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, errNotFound
		}
		return nil, err
	}
	return &e, nil
}

func (s Store) ExportByKey(ctx context.Context, vendor, key string) (*Export, error) {
	return scanExport(s.Conn(ctx).QueryRow(ctx, `SELECT `+exportColumns+` FROM import_exports WHERE vendor_id=$1 AND idempotency_key=$2`, vendor, key))
}

func (s Store) Export(ctx context.Context, id string) (*Export, error) {
	return scanExport(s.Conn(ctx).QueryRow(ctx, `SELECT `+exportColumns+` FROM import_exports WHERE id=$1`, id))
}

func (s Store) CreateExport(ctx context.Context, e *Export) error {
	err := s.Conn(ctx).QueryRow(ctx, `INSERT INTO import_exports(id,vendor_id,kind,requested_by,idempotency_key,object_key) VALUES($1,$2,$3,$4,$5,$6) RETURNING status,created_at`,
		e.ID, e.VendorID, e.Kind, e.RequestedBy, e.idempotencyKey, e.objectKey).Scan(&e.Status, &e.CreatedAt)
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) && pgErr.Code == "23505" {
		return errDuplicateKey
	}
	return err
}

func (s Store) ClaimExport(ctx context.Context) (*Export, error) {
	return scanExport(s.Conn(ctx).QueryRow(ctx, `UPDATE import_exports SET status='processing',attempts=attempts+1,next_attempt_at=now()+interval '3 minutes'
 WHERE id=(SELECT id FROM import_exports WHERE status IN ('pending','processing') AND next_attempt_at<=now() ORDER BY next_attempt_at,id LIMIT 1 FOR UPDATE SKIP LOCKED)
 RETURNING `+exportColumns))
}

// FinishExport records the outcome of one attempt; a failed attempt is
// retried with backoff until the attempt budget is spent.
func (s Store) FinishExport(ctx context.Context, e *Export, rowsWritten int, code string, retryIn time.Duration) error {
	if code == "" {
		_, err := s.Conn(ctx).Exec(ctx, `UPDATE import_exports SET status='ready',row_count=$3,expires_at=now()+make_interval(secs=>$4) WHERE id=$1 AND attempts=$2 AND status='processing'`,
			e.ID, e.attempts, rowsWritten, ReportRetention.Seconds())
		return err
	}
	_, err := s.Conn(ctx).Exec(ctx, `UPDATE import_exports SET status=CASE WHEN $4>0 AND attempts<$5 THEN 'pending' ELSE 'failed' END,
 error_code=$3,next_attempt_at=now()+make_interval(secs=>$4) WHERE id=$1 AND attempts=$2 AND status='processing'`, e.ID, e.attempts, code, retryIn.Seconds(), MaxApplyAttempts)
	return err
}

func (s Store) ExpiredExports(ctx context.Context, limit int) ([]storedFile, error) {
	rows, err := s.Conn(ctx).Query(ctx, `SELECT id,object_key FROM import_exports WHERE status='ready' AND expires_at<now() ORDER BY expires_at LIMIT $1`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []storedFile
	for rows.Next() {
		var f storedFile
		if err = rows.Scan(&f.ID, &f.Key); err != nil {
			return nil, err
		}
		out = append(out, f)
	}
	return out, rows.Err()
}

func (s Store) MarkExportExpired(ctx context.Context, id string) error {
	_, err := s.Conn(ctx).Exec(ctx, `UPDATE import_exports SET status='expired' WHERE id=$1 AND status='ready'`, id)
	return err
}

// BacklogSQL is the confirmed rows still to apply and how long the oldest
// has waited, in the shape of telemetry.Outbox.
const BacklogSQL = `SELECT count(*),COALESCE(EXTRACT(EPOCH FROM now()-min(r.updated_at)),0)::float8 FROM import_rows r JOIN import_jobs j ON j.id=r.job_id
 WHERE r.status IN ('valid','applying') AND j.status IN ('confirmed','applying','cancelling')`

var errInvalidCursor = Problem(400, "validation_error", "Invalid cursor")
