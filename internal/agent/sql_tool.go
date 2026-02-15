package agent

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"time"

	// Database drivers
	_ "github.com/go-sql-driver/mysql"
	_ "github.com/lib/pq"
	_ "github.com/mattn/go-sqlite3"

	"trpc.group/trpc-go/trpc-agent-go/tool"
	"trpc.group/trpc-go/trpc-agent-go/tool/function"

	"github.com/yourusername/kaggen/internal/config"
	"github.com/yourusername/kaggen/internal/secrets"
)

const (
	sqlDefaultTimeout = 30 * time.Second
	sqlMaxTimeout     = 5 * time.Minute
	sqlMaxRows        = 1000  // Max rows to return per query
	sqlMaxConns       = 5     // Default max connections per database
)

// SQLToolArgs defines the input arguments for the sql tool.
type SQLToolArgs struct {
	// Action selection
	Action string `json:"action" jsonschema:"required,description=Action to perform: query (SELECT) execute (INSERT/UPDATE/DELETE) tables (list tables) describe (table schema),enum=query,enum=execute,enum=tables,enum=describe"`

	// Connection
	Connection string `json:"connection" jsonschema:"required,description=Database connection name (defined in config)"`

	// Query execution
	Query  string `json:"query,omitempty" jsonschema:"description=SQL query to execute. Required for query/execute actions. Use parameterized queries ($1 $2 for postgres or ? for mysql/sqlite)."`
	Params []any  `json:"params,omitempty" jsonschema:"description=Query parameters for parameterized queries. Prevents SQL injection."`

	// Describe action
	Table string `json:"table,omitempty" jsonschema:"description=Table name to describe. Required for describe action."`

	// Options
	TimeoutSecs int `json:"timeout_seconds,omitempty" jsonschema:"description=Query timeout in seconds (default: 30 max: 300)."`
	Limit       int `json:"limit,omitempty" jsonschema:"description=Maximum rows to return (default: 1000)."`
}

// SQLToolResult is the result of a SQL operation.
type SQLToolResult struct {
	Success bool   `json:"success"`
	Message string `json:"message"`

	// Query results
	Columns []string         `json:"columns,omitempty"` // Column names
	Rows    []map[string]any `json:"rows,omitempty"`    // Result rows
	Count   int              `json:"count,omitempty"`   // Row count (query) or affected rows (execute)

	// Schema info
	Tables      []string       `json:"tables,omitempty"`       // For tables action
	TableSchema []ColumnSchema `json:"table_schema,omitempty"` // For describe action
}

// ColumnSchema represents a database column.
type ColumnSchema struct {
	Name     string `json:"name"`
	Type     string `json:"type"`
	Nullable bool   `json:"nullable"`
	Key      string `json:"key,omitempty"` // PRI, UNI, MUL, etc.
	Default  string `json:"default,omitempty"`
}

// SQLConnectionManager manages database connections.
type SQLConnectionManager struct {
	mu          sync.RWMutex
	connections map[string]*sql.DB
	configs     map[string]config.DatabaseConnection
}

// NewSQLConnectionManager creates a new SQL connection manager.
func NewSQLConnectionManager() *SQLConnectionManager {
	return &SQLConnectionManager{
		connections: make(map[string]*sql.DB),
		configs:     make(map[string]config.DatabaseConnection),
	}
}

// RegisterConnection registers a database connection configuration.
func (m *SQLConnectionManager) RegisterConnection(name string, cfg config.DatabaseConnection) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.configs[name] = cfg
}

// GetConnection returns a database connection, creating it if needed.
func (m *SQLConnectionManager) GetConnection(name string) (*sql.DB, config.DatabaseConnection, error) {
	m.mu.RLock()
	cfg, ok := m.configs[name]
	db := m.connections[name]
	m.mu.RUnlock()

	if !ok {
		return nil, config.DatabaseConnection{}, fmt.Errorf("connection %q not configured", name)
	}

	if db != nil {
		// Test if connection is still alive
		if err := db.Ping(); err == nil {
			return db, cfg, nil
		}
		// Connection is dead, close and recreate
		db.Close()
	}

	// Create new connection
	db, err := m.createConnection(name, cfg)
	if err != nil {
		return nil, cfg, err
	}

	m.mu.Lock()
	m.connections[name] = db
	m.mu.Unlock()

	return db, cfg, nil
}

func (m *SQLConnectionManager) createConnection(name string, cfg config.DatabaseConnection) (*sql.DB, error) {
	var dsn string
	var driverName string

	// Resolve password from secrets if needed
	password := cfg.Password
	if strings.HasPrefix(password, "secret:") {
		secretName := strings.TrimPrefix(password, "secret:")
		store := secrets.DefaultStore()
		val, err := store.Get(secretName)
		if err != nil {
			return nil, fmt.Errorf("failed to get secret %q: %w", secretName, err)
		}
		password = val
	}

	switch cfg.Driver {
	case "postgres", "postgresql":
		driverName = "postgres"
		sslMode := cfg.SSLMode
		if sslMode == "" {
			sslMode = "disable"
		}
		dsn = fmt.Sprintf("host=%s port=%d user=%s password=%s dbname=%s sslmode=%s",
			cfg.Host, cfg.Port, cfg.User, password, cfg.Database, sslMode)

	case "mysql":
		driverName = "mysql"
		// MySQL DSN format: user:password@tcp(host:port)/database
		dsn = fmt.Sprintf("%s:%s@tcp(%s:%d)/%s?parseTime=true",
			cfg.User, password, cfg.Host, cfg.Port, cfg.Database)
		if cfg.SSLMode != "" && cfg.SSLMode != "disable" {
			dsn += "&tls=" + cfg.SSLMode
		}

	case "sqlite", "sqlite3":
		driverName = "sqlite3"
		dsn = config.ExpandPath(cfg.Database)

	default:
		return nil, fmt.Errorf("unsupported driver %q (supported: postgres, mysql, sqlite)", cfg.Driver)
	}

	db, err := sql.Open(driverName, dsn)
	if err != nil {
		return nil, fmt.Errorf("failed to open database: %w", err)
	}

	// Configure connection pool
	maxConns := cfg.MaxConns
	if maxConns <= 0 {
		maxConns = sqlMaxConns
	}
	db.SetMaxOpenConns(maxConns)
	db.SetMaxIdleConns(maxConns / 2)
	db.SetConnMaxLifetime(5 * time.Minute)

	// Test connection
	if err := db.Ping(); err != nil {
		db.Close()
		return nil, fmt.Errorf("failed to connect to database: %w", err)
	}

	return db, nil
}

// Close closes all database connections.
func (m *SQLConnectionManager) Close() {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, db := range m.connections {
		db.Close()
	}
	m.connections = make(map[string]*sql.DB)
}

// ConnectionNames returns a list of registered connection names.
func (m *SQLConnectionManager) ConnectionNames() []string {
	m.mu.RLock()
	defer m.mu.RUnlock()
	names := make([]string, 0, len(m.configs))
	for name := range m.configs {
		names = append(names, name)
	}
	return names
}

// NewSQLTool creates a SQL tool with the given connection manager.
func NewSQLTool(manager *SQLConnectionManager) tool.CallableTool {
	return function.NewFunctionTool(
		func(ctx context.Context, args SQLToolArgs) (*SQLToolResult, error) {
			return executeSQLTool(ctx, args, manager)
		},
		function.WithName("sql"),
		function.WithDescription("Execute SQL queries against configured databases. Actions: query (SELECT), execute (INSERT/UPDATE/DELETE), tables (list tables), describe (table schema). Use parameterized queries with params array to prevent SQL injection."),
	)
}

func executeSQLTool(
	ctx context.Context,
	args SQLToolArgs,
	manager *SQLConnectionManager,
) (*SQLToolResult, error) {
	result := &SQLToolResult{}

	// Get database connection
	db, cfg, err := manager.GetConnection(args.Connection)
	if err != nil {
		result.Message = fmt.Sprintf("Connection error: %v", err)
		return result, nil
	}

	// Set timeout
	timeout := sqlDefaultTimeout
	if args.TimeoutSecs > 0 {
		timeout = time.Duration(args.TimeoutSecs) * time.Second
		if timeout > sqlMaxTimeout {
			timeout = sqlMaxTimeout
		}
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	switch args.Action {
	case "query":
		return executeQuery(ctx, db, cfg, args)
	case "execute":
		return executeStatement(ctx, db, cfg, args)
	case "tables":
		return listTables(ctx, db, cfg)
	case "describe":
		return describeTable(ctx, db, cfg, args)
	default:
		result.Message = fmt.Sprintf("Unknown action %q. Use: query, execute, tables, describe", args.Action)
		return result, nil
	}
}

func executeQuery(ctx context.Context, db *sql.DB, cfg config.DatabaseConnection, args SQLToolArgs) (*SQLToolResult, error) {
	result := &SQLToolResult{}

	if args.Query == "" {
		result.Message = "Error: 'query' is required for query action"
		return result, nil
	}

	// Validate read-only mode
	if cfg.ReadOnly {
		queryUpper := strings.ToUpper(strings.TrimSpace(args.Query))
		if !strings.HasPrefix(queryUpper, "SELECT") && !strings.HasPrefix(queryUpper, "WITH") {
			result.Message = "Error: connection is read-only, only SELECT queries are allowed"
			return result, nil
		}
	}

	// Execute query
	rows, err := db.QueryContext(ctx, args.Query, args.Params...)
	if err != nil {
		if ctx.Err() == context.DeadlineExceeded {
			result.Message = "Query timed out"
			return result, nil
		}
		result.Message = fmt.Sprintf("Query error: %v", err)
		return result, nil
	}
	defer rows.Close()

	// Get column names
	columns, err := rows.Columns()
	if err != nil {
		result.Message = fmt.Sprintf("Error getting columns: %v", err)
		return result, nil
	}
	result.Columns = columns

	// Determine row limit
	limit := args.Limit
	if limit <= 0 || limit > sqlMaxRows {
		limit = sqlMaxRows
	}

	// Fetch rows
	result.Rows = make([]map[string]any, 0)
	for rows.Next() && len(result.Rows) < limit {
		// Create a slice of interface{} to hold column values
		values := make([]any, len(columns))
		valuePtrs := make([]any, len(columns))
		for i := range values {
			valuePtrs[i] = &values[i]
		}

		if err := rows.Scan(valuePtrs...); err != nil {
			result.Message = fmt.Sprintf("Error scanning row: %v", err)
			return result, nil
		}

		// Convert to map
		row := make(map[string]any)
		for i, col := range columns {
			val := values[i]
			// Handle byte arrays (common for some database types)
			if b, ok := val.([]byte); ok {
				row[col] = string(b)
			} else {
				row[col] = val
			}
		}
		result.Rows = append(result.Rows, row)
	}

	if err := rows.Err(); err != nil {
		result.Message = fmt.Sprintf("Error iterating rows: %v", err)
		return result, nil
	}

	result.Success = true
	result.Count = len(result.Rows)
	if result.Count == limit {
		result.Message = fmt.Sprintf("Returned %d rows (limit reached)", result.Count)
	} else {
		result.Message = fmt.Sprintf("Returned %d rows", result.Count)
	}

	return result, nil
}

func executeStatement(ctx context.Context, db *sql.DB, cfg config.DatabaseConnection, args SQLToolArgs) (*SQLToolResult, error) {
	result := &SQLToolResult{}

	if args.Query == "" {
		result.Message = "Error: 'query' is required for execute action"
		return result, nil
	}

	// Check read-only mode
	if cfg.ReadOnly {
		result.Message = "Error: connection is read-only, write operations are not allowed"
		return result, nil
	}

	// Validate that this looks like a write statement
	queryUpper := strings.ToUpper(strings.TrimSpace(args.Query))
	if strings.HasPrefix(queryUpper, "SELECT") {
		result.Message = "Error: use 'query' action for SELECT statements"
		return result, nil
	}

	// Execute statement
	res, err := db.ExecContext(ctx, args.Query, args.Params...)
	if err != nil {
		if ctx.Err() == context.DeadlineExceeded {
			result.Message = "Statement timed out"
			return result, nil
		}
		result.Message = fmt.Sprintf("Execute error: %v", err)
		return result, nil
	}

	affected, _ := res.RowsAffected()
	result.Success = true
	result.Count = int(affected)
	result.Message = fmt.Sprintf("Statement executed, %d row(s) affected", affected)

	return result, nil
}

func listTables(ctx context.Context, db *sql.DB, cfg config.DatabaseConnection) (*SQLToolResult, error) {
	result := &SQLToolResult{}

	var query string
	switch cfg.Driver {
	case "postgres", "postgresql":
		query = `SELECT table_name FROM information_schema.tables
				 WHERE table_schema = 'public' AND table_type = 'BASE TABLE'
				 ORDER BY table_name`
	case "mysql":
		query = `SELECT table_name FROM information_schema.tables
				 WHERE table_schema = DATABASE() AND table_type = 'BASE TABLE'
				 ORDER BY table_name`
	case "sqlite", "sqlite3":
		query = `SELECT name FROM sqlite_master
				 WHERE type = 'table' AND name NOT LIKE 'sqlite_%'
				 ORDER BY name`
	default:
		result.Message = fmt.Sprintf("Unsupported driver for tables action: %s", cfg.Driver)
		return result, nil
	}

	rows, err := db.QueryContext(ctx, query)
	if err != nil {
		result.Message = fmt.Sprintf("Error listing tables: %v", err)
		return result, nil
	}
	defer rows.Close()

	tables := make([]string, 0)
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			result.Message = fmt.Sprintf("Error scanning table name: %v", err)
			return result, nil
		}
		tables = append(tables, name)
	}

	result.Success = true
	result.Tables = tables
	result.Message = fmt.Sprintf("Found %d table(s)", len(tables))

	return result, nil
}

func describeTable(ctx context.Context, db *sql.DB, cfg config.DatabaseConnection, args SQLToolArgs) (*SQLToolResult, error) {
	result := &SQLToolResult{}

	if args.Table == "" {
		result.Message = "Error: 'table' is required for describe action"
		return result, nil
	}

	var query string
	switch cfg.Driver {
	case "postgres", "postgresql":
		query = `SELECT column_name, data_type, is_nullable, column_default,
				 CASE WHEN pk.column_name IS NOT NULL THEN 'PRI' ELSE '' END as key_type
				 FROM information_schema.columns c
				 LEFT JOIN (
					 SELECT kcu.column_name
					 FROM information_schema.table_constraints tc
					 JOIN information_schema.key_column_usage kcu
					   ON tc.constraint_name = kcu.constraint_name
					 WHERE tc.table_name = $1 AND tc.constraint_type = 'PRIMARY KEY'
				 ) pk ON c.column_name = pk.column_name
				 WHERE c.table_name = $1 AND c.table_schema = 'public'
				 ORDER BY c.ordinal_position`
	case "mysql":
		query = `DESCRIBE ` + args.Table // MySQL DESCRIBE is safe for table names
	case "sqlite", "sqlite3":
		query = `PRAGMA table_info(` + args.Table + `)`
	default:
		result.Message = fmt.Sprintf("Unsupported driver for describe action: %s", cfg.Driver)
		return result, nil
	}

	var rows *sql.Rows
	var err error
	if cfg.Driver == "postgres" || cfg.Driver == "postgresql" {
		rows, err = db.QueryContext(ctx, query, args.Table)
	} else {
		rows, err = db.QueryContext(ctx, query)
	}
	if err != nil {
		result.Message = fmt.Sprintf("Error describing table: %v", err)
		return result, nil
	}
	defer rows.Close()

	schema := make([]ColumnSchema, 0)

	switch cfg.Driver {
	case "postgres", "postgresql":
		for rows.Next() {
			var col ColumnSchema
			var nullable, defaultVal sql.NullString
			if err := rows.Scan(&col.Name, &col.Type, &nullable, &defaultVal, &col.Key); err != nil {
				result.Message = fmt.Sprintf("Error scanning column: %v", err)
				return result, nil
			}
			col.Nullable = nullable.String == "YES"
			col.Default = defaultVal.String
			schema = append(schema, col)
		}
	case "mysql":
		for rows.Next() {
			var col ColumnSchema
			var nullable, key, extra sql.NullString
			var defaultVal sql.NullString
			if err := rows.Scan(&col.Name, &col.Type, &nullable, &key, &defaultVal, &extra); err != nil {
				result.Message = fmt.Sprintf("Error scanning column: %v", err)
				return result, nil
			}
			col.Nullable = nullable.String == "YES"
			col.Key = key.String
			col.Default = defaultVal.String
			schema = append(schema, col)
		}
	case "sqlite", "sqlite3":
		for rows.Next() {
			var col ColumnSchema
			var cid int
			var notNull, pk int
			var defaultVal sql.NullString
			if err := rows.Scan(&cid, &col.Name, &col.Type, &notNull, &defaultVal, &pk); err != nil {
				result.Message = fmt.Sprintf("Error scanning column: %v", err)
				return result, nil
			}
			col.Nullable = notNull == 0
			col.Default = defaultVal.String
			if pk == 1 {
				col.Key = "PRI"
			}
			schema = append(schema, col)
		}
	}

	result.Success = true
	result.TableSchema = schema
	result.Message = fmt.Sprintf("Table %q has %d column(s)", args.Table, len(schema))

	// Also return as JSON for easier inspection
	if schemaJSON, err := json.MarshalIndent(schema, "", "  "); err == nil {
		result.Message += "\n" + string(schemaJSON)
	}

	return result, nil
}
