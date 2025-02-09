package collector

import (
	"context"
	"database/sql"
	"strings"
	"sync"
	"time"

	"github.com/cprobe/cprobe/lib/conv"
	"github.com/cprobe/cprobe/lib/logger"
	"github.com/cprobe/cprobe/types"
)

type CustomQuery struct {
	Mesurement    string        `toml:"mesurement"`
	MetricFields  []string      `toml:"metric_fields"`
	LabelFields   []string      `toml:"label_fields"`
	FieldToAppend string        `toml:"field_to_append"`
	Timeout       time.Duration `toml:"timeout"`
	Request       string        `toml:"request"`
}

func (e *Exporter) collectCustomQueries(ctx context.Context, db *sql.DB, ss *types.Samples, queries []CustomQuery) {
	if len(queries) == 0 {
		return
	}

	wg := new(sync.WaitGroup)
	defer wg.Wait()

	for i := 0; i < len(queries); i++ {
		wg.Add(1)
		go func(query CustomQuery) {
			defer wg.Done()
			e.collectCustomQuery(ctx, db, ss, query)
		}(queries[i])
	}
}

func (e *Exporter) collectCustomQuery(ctx context.Context, db *sql.DB, ss *types.Samples, query CustomQuery) {
	ctx, cancel := context.WithTimeout(ctx, query.Timeout)
	defer cancel()

	rows, err := db.QueryContext(ctx, query.Request)
	if ctx.Err() == context.DeadlineExceeded {
		logger.Errorf("query timeout, request: %s", query.Request)
		return
	}

	if err != nil {
		logger.Errorf("failed to query: %s, error: %s", query.Request, err)
		return
	}

	defer rows.Close()

	cols, err := rows.Columns()
	if err != nil {
		logger.Errorf("failed to get columns: %s", err)
		return
	}

	for rows.Next() {
		columns := make([]sql.RawBytes, len(cols))
		columnPointers := make([]interface{}, len(cols))
		for i := range columns {
			columnPointers[i] = &columns[i]
		}

		// Scan the result into the column pointers...
		if err := rows.Scan(columnPointers...); err != nil {
			logger.Errorf("failed to scan: %s", err)
			return
		}

		row := make(map[string]string)
		for i, colName := range cols {
			val := columnPointers[i].(*sql.RawBytes)
			row[strings.ToLower(colName)] = string(*val)
		}

		if err = e.parseRow(row, query, ss); err != nil {
			logger.Errorf("failed to parse row: %s, sql: %s", err, query.Request)
		}
	}
}

func (e *Exporter) parseRow(row map[string]string, query CustomQuery, ss *types.Samples) error {
	labels := make(map[string]string)

	for _, label := range query.LabelFields {
		labelValue, has := row[label]
		if has {
			labels[label] = strings.Replace(labelValue, " ", "_", -1)
		}
	}

	for _, column := range query.MetricFields {
		value, err := conv.ToFloat64(row[column])
		if err != nil {
			logger.Errorf("failed to convert field: %s, value: %v, error: %s", column, row[column], err)
			return err
		}

		if query.FieldToAppend == "" {
			ss.AddMetric(query.Mesurement, map[string]interface{}{
				column: value,
			}, labels)
		} else {
			suffix := cleanName(row[query.FieldToAppend])
			ss.AddMetric(query.Mesurement+"_"+suffix, map[string]interface{}{
				column: value,
			}, labels)
		}
	}

	return nil
}

func cleanName(s string) string {
	s = strings.Replace(s, " ", "_", -1) // Remove spaces
	s = strings.Replace(s, "(", "", -1)  // Remove open parenthesis
	s = strings.Replace(s, ")", "", -1)  // Remove close parenthesis
	s = strings.Replace(s, "/", "", -1)  // Remove forward slashes
	s = strings.Replace(s, "*", "", -1)  // Remove asterisks
	s = strings.Replace(s, "%", "percent", -1)
	s = strings.ToLower(s)
	return s
}
