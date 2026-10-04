// Copyright 2026 Simone Vellei
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package registry

import (
	"database/sql"
	"errors"
	"fmt"
	"sync"

	// The PostgreSQL driver phero's psql memory and vector store expect.
	_ "github.com/jackc/pgx/v5/stdlib"
)

// sqlDriver is the database/sql driver used for every DSN.
const sqlDriver = "pgx"

// SQLPool shares one *sql.DB per DSN: memories and vector stores built per
// job reuse the same connection pool.
type SQLPool struct {
	mu  sync.Mutex
	dbs map[string]*sql.DB
}

// NewSQLPool returns an empty pool.
func NewSQLPool() *SQLPool { return &SQLPool{dbs: map[string]*sql.DB{}} }

// Open returns the handle of dsn, opening it on first use.
func (p *SQLPool) Open(dsn string) (*sql.DB, error) {
	p.mu.Lock()
	defer p.mu.Unlock()

	if db, ok := p.dbs[dsn]; ok {
		return db, nil
	}

	db, err := sql.Open(sqlDriver, dsn)
	if err != nil {
		return nil, fmt.Errorf("registry: open database: %w", err)
	}

	p.dbs[dsn] = db

	return db, nil
}

// Close closes every handle.
func (p *SQLPool) Close() error {
	p.mu.Lock()
	defer p.mu.Unlock()

	errs := make([]error, 0, len(p.dbs))

	for dsn, db := range p.dbs {
		errs = append(errs, db.Close())

		delete(p.dbs, dsn)
	}

	return errors.Join(errs...)
}
