import Database from "better-sqlite3";
import { drizzle, BetterSQLite3Database } from "drizzle-orm/better-sqlite3";
import * as schema from "./schema";

const databasePath = process.env.DATABASE_PATH || "/data/todos.db";

let _db: BetterSQLite3Database<typeof schema> | null = null;

function initDatabase(): BetterSQLite3Database<typeof schema> {
  if (_db) return _db;

  const sqlite = new Database(databasePath);

  // Enable WAL mode for better performance with Litestream
  sqlite.pragma("journal_mode = WAL");
  sqlite.pragma("busy_timeout = 5000");
  sqlite.pragma("synchronous = NORMAL");
  sqlite.pragma("cache_size = -2000"); // 2MB cache (negative value = KB)
  sqlite.pragma("foreign_keys = true");
  sqlite.pragma("temp_store = memory");

  _db = drizzle(sqlite, { schema });
  return _db;
}

export const db = new Proxy({} as BetterSQLite3Database<typeof schema>, {
  get(_, prop) {
    const database = initDatabase();
    return (database as unknown as Record<string | symbol, unknown>)[prop];
  },
});
