package db

import (
	"context"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

func SeedAppSettingIfMissing(ctx context.Context, pool *pgxpool.Pool, key, value string) error {
	_, err := pool.Exec(ctx, `INSERT INTO app_settings (key, value) VALUES ($1, $2)
		ON CONFLICT (key) DO UPDATE SET value = EXCLUDED.value, updated_at = NOW()
		WHERE app_settings.value = ''`, key, value)
	return err
}

func UpsertAppSetting(ctx context.Context, pool *pgxpool.Pool, key, value string) (AppSetting, error) {
	var s AppSetting
	err := pool.QueryRow(ctx, `INSERT INTO app_settings (key, value) VALUES ($1, $2)
		ON CONFLICT (key) DO UPDATE SET value = EXCLUDED.value, updated_at = NOW()
		RETURNING key, value, updated_at`, key, value).Scan(&s.Key, &s.Value, &s.UpdatedAt)
	return s, err
}

func GetAppSetting(ctx context.Context, pool *pgxpool.Pool, key string) (AppSetting, error) {
	var s AppSetting
	err := pool.QueryRow(ctx, `SELECT key, value, updated_at FROM app_settings WHERE key = $1`, key).Scan(&s.Key, &s.Value, &s.UpdatedAt)
	if err == pgx.ErrNoRows {
		return s, ErrNotFound
	}
	return s, err
}

func ListAppSettings(ctx context.Context, pool *pgxpool.Pool) ([]AppSetting, error) {
	rows, err := pool.Query(ctx, `SELECT key, value, updated_at FROM app_settings ORDER BY key`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []AppSetting
	for rows.Next() {
		var s AppSetting
		if err := rows.Scan(&s.Key, &s.Value, &s.UpdatedAt); err != nil {
			return nil, err
		}
		out = append(out, s)
	}
	return out, rows.Err()
}
