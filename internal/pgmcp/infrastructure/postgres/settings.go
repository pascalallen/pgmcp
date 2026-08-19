package postgres

import (
	"context"
	"database/sql"
	"fmt"

	"github.com/pascalallen/pgmcp/internal/pgmcp/domain/diagnostics"
)

// settingsQuery reads every server setting with its unit, where it came from
// and what it does.
const settingsQuery = `
SELECT name,
       setting,
       coalesce(unit, ''),
       source,
       category,
       short_desc
FROM pg_settings
ORDER BY name`

// Settings returns every server setting, ordered by name.
func (s *Store) Settings(ctx context.Context) ([]diagnostics.Setting, error) {
	settings := make([]diagnostics.Setting, 0)

	if err := s.readOnly(ctx, 0, func(ctx context.Context, tx *sql.Tx) error {
		rows, err := tx.QueryContext(ctx, settingsQuery)
		if err != nil {
			return fmt.Errorf("postgres: settings: %w", err)
		}
		defer func() { _ = rows.Close() }()

		for rows.Next() {
			var (
				setting   diagnostics.Setting
				value     sql.NullString
				unit      sql.NullString
				source    sql.NullString
				category  sql.NullString
				shortDesc sql.NullString
			)
			if err := rows.Scan(&setting.Name, &value, &unit, &source, &category, &shortDesc); err != nil {
				return fmt.Errorf("postgres: scan setting: %w", err)
			}

			setting.Value = value.String
			setting.Unit = unit.String
			setting.Source = source.String
			setting.Category = category.String
			setting.ShortDesc = shortDesc.String

			settings = append(settings, setting)
		}

		return rows.Err()
	}); err != nil {
		return nil, err
	}

	return settings, nil
}
