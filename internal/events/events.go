// Package events grava e lista a trilha de auditoria (TSD §13.4). Nunca recebe valores de segredos.
package events

import (
	"context"
	"database/sql"
	"encoding/json"
	"log/slog"
	"time"
)

type Event struct {
	ID      int64           `json:"id"`
	TS      time.Time       `json:"ts"`
	Level   string          `json:"level"`
	Kind    string          `json:"kind"`
	AppID   *int64          `json:"app_id"`
	Message string          `json:"message"`
	Data    json.RawMessage `json:"data,omitempty"`
}

type Log struct {
	DB  *sql.DB
	Log *slog.Logger
}

// Add nunca falha o chamador: a auditoria não pode derrubar a operação.
func (l *Log) Add(ctx context.Context, level, kind string, appID *int64, msg string, data any) {
	var d any
	if data != nil {
		b, _ := json.Marshal(data)
		d = string(b)
	}
	if _, err := l.DB.ExecContext(ctx, `insert into events(ts,level,kind,app_id,message,data) values(?,?,?,?,?,?)`,
		time.Now().UnixMilli(), level, kind, appID, msg, d); err != nil && l.Log != nil {
		l.Log.Error("falha ao gravar evento", "kind", kind, "err", err)
	}
}

func (l *Log) Info(ctx context.Context, kind string, appID *int64, msg string, data any) {
	l.Add(ctx, "info", kind, appID, msg, data)
}
func (l *Log) Warn(ctx context.Context, kind string, appID *int64, msg string, data any) {
	l.Add(ctx, "warn", kind, appID, msg, data)
}
func (l *Log) Error(ctx context.Context, kind string, appID *int64, msg string, data any) {
	l.Add(ctx, "error", kind, appID, msg, data)
}

// List devolve do mais novo para o mais antigo; before=0 começa do fim.
func (l *Log) List(ctx context.Context, limit int, before int64) ([]Event, error) {
	if before <= 0 {
		before = 1<<62 - 1
	}
	rows, err := l.DB.QueryContext(ctx, `select id,ts,level,kind,app_id,message,coalesce(data,'') from events where id<? order by id desc limit ?`, before, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Event{}
	for rows.Next() {
		var e Event
		var ts int64
		var data string
		if err := rows.Scan(&e.ID, &ts, &e.Level, &e.Kind, &e.AppID, &e.Message, &data); err != nil {
			return nil, err
		}
		e.TS = time.UnixMilli(ts).UTC()
		if data != "" {
			e.Data = json.RawMessage(data)
		}
		out = append(out, e)
	}
	return out, rows.Err()
}
