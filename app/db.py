import os
import json
import sqlite3
import threading
from datetime import datetime, timezone
from typing import Optional, List, Dict, Any


def _utc_now() -> str:
    return datetime.now(timezone.utc).isoformat()


class GatewayDB:
    """SQLite persistence for resumable jobs, SSE event buffers, and local Ollama chats."""

    def __init__(self, db_path: str):
        self.db_path = db_path
        os.makedirs(os.path.dirname(db_path), exist_ok=True)
        self._lock = threading.Lock()
        self._init_schema()

    def _connect(self) -> sqlite3.Connection:
        conn = sqlite3.connect(self.db_path, check_same_thread=False, timeout=10.0)
        conn.row_factory = sqlite3.Row
        conn.execute("PRAGMA journal_mode=WAL;")
        conn.execute("PRAGMA synchronous=NORMAL;")
        conn.execute("PRAGMA foreign_keys=ON;")
        return conn

    def _init_schema(self) -> None:
        with self._lock, self._connect() as conn:
            conn.executescript(
                """
                CREATE TABLE IF NOT EXISTS jobs (
                    id TEXT PRIMARY KEY,
                    conversation_id TEXT,
                    engine TEXT NOT NULL,
                    model TEXT NOT NULL,
                    permission_mode TEXT NOT NULL,
                    prompt TEXT NOT NULL,
                    status TEXT NOT NULL DEFAULT 'running',
                    error TEXT,
                    created_at TEXT NOT NULL,
                    updated_at TEXT NOT NULL
                );

                CREATE TABLE IF NOT EXISTS job_events (
                    id INTEGER PRIMARY KEY AUTOINCREMENT,
                    job_id TEXT NOT NULL,
                    seq INTEGER NOT NULL,
                    event_type TEXT NOT NULL,
                    payload TEXT NOT NULL,
                    created_at TEXT NOT NULL,
                    FOREIGN KEY (job_id) REFERENCES jobs(id) ON DELETE CASCADE
                );

                CREATE INDEX IF NOT EXISTS idx_job_events_job_seq
                    ON job_events(job_id, seq);

                CREATE TABLE IF NOT EXISTS local_conversations (
                    id TEXT PRIMARY KEY,
                    title TEXT NOT NULL,
                    model TEXT NOT NULL,
                    created_at TEXT NOT NULL,
                    updated_at TEXT NOT NULL
                );

                CREATE TABLE IF NOT EXISTS local_messages (
                    id INTEGER PRIMARY KEY AUTOINCREMENT,
                    conversation_id TEXT NOT NULL,
                    role TEXT NOT NULL,
                    content TEXT NOT NULL,
                    created_at TEXT NOT NULL,
                    FOREIGN KEY (conversation_id) REFERENCES local_conversations(id) ON DELETE CASCADE
                );

                CREATE INDEX IF NOT EXISTS idx_local_messages_conv
                    ON local_messages(conversation_id, id);

                CREATE TABLE IF NOT EXISTS settings (
                    key TEXT PRIMARY KEY,
                    value TEXT NOT NULL,
                    updated_at TEXT NOT NULL
                );

                CREATE TABLE IF NOT EXISTS push_subscriptions (
                    endpoint TEXT PRIMARY KEY,
                    subscription_json TEXT NOT NULL,
                    created_at TEXT NOT NULL
                );

                CREATE VIRTUAL TABLE IF NOT EXISTS conversations_fts
                    USING fts5(conv_id UNINDEXED, title, content, tokenize='unicode61');
                """
            )
            # Mark orphaned running jobs from a previous container crash as interrupted
            conn.execute(
                "UPDATE jobs SET status = 'error', error = 'Gateway reiniciado durante execução', updated_at = ? WHERE status = 'running'",
                (_utc_now(),),
            )

    def create_job(
        self,
        job_id: str,
        conversation_id: Optional[str],
        engine: str,
        model: str,
        permission_mode: str,
        prompt: str,
    ) -> Dict[str, Any]:
        now = _utc_now()
        with self._lock, self._connect() as conn:
            conn.execute(
                """
                INSERT INTO jobs (id, conversation_id, engine, model, permission_mode, prompt, status, created_at, updated_at)
                VALUES (?, ?, ?, ?, ?, ?, 'running', ?, ?)
                """,
                (job_id, conversation_id, engine, model, permission_mode, prompt, now, now),
            )
        return self.get_job(job_id)  # type: ignore

    def update_job(
        self,
        job_id: str,
        *,
        conversation_id: Optional[str] = None,
        status: Optional[str] = None,
        error: Optional[str] = None,
    ) -> None:
        fields = ["updated_at = ?"]
        values: List[Any] = [_utc_now()]
        if conversation_id is not None:
            fields.append("conversation_id = ?")
            values.append(conversation_id)
        if status is not None:
            fields.append("status = ?")
            values.append(status)
        if error is not None:
            fields.append("error = ?")
            values.append(error)
        values.append(job_id)
        with self._lock, self._connect() as conn:
            conn.execute(f"UPDATE jobs SET {', '.join(fields)} WHERE id = ?", values)

    def get_job(self, job_id: str) -> Optional[Dict[str, Any]]:
        with self._lock, self._connect() as conn:
            row = conn.execute("SELECT * FROM jobs WHERE id = ?", (job_id,)).fetchone()
            if not row:
                return None
            last_seq_row = conn.execute(
                "SELECT COALESCE(MAX(seq), 0) AS max_seq FROM job_events WHERE job_id = ?",
                (job_id,),
            ).fetchone()
            res = dict(row)
            res["last_seq"] = last_seq_row["max_seq"] if last_seq_row else 0
            return res

    def get_active_jobs(self) -> List[Dict[str, Any]]:
        with self._lock, self._connect() as conn:
            rows = conn.execute(
                "SELECT * FROM jobs WHERE status = 'running' ORDER BY created_at DESC LIMIT 10"
            ).fetchall()
            return [dict(r) for r in rows]

    def append_job_event(self, job_id: str, event_type: str, payload: Dict[str, Any]) -> int:
        now = _utc_now()
        payload_str = json.dumps(payload, ensure_ascii=False)
        with self._lock, self._connect() as conn:
            row = conn.execute(
                "SELECT COALESCE(MAX(seq), 0) + 1 AS next_seq FROM job_events WHERE job_id = ?",
                (job_id,),
            ).fetchone()
            seq = int(row["next_seq"])
            conn.execute(
                """
                INSERT INTO job_events (job_id, seq, event_type, payload, created_at)
                VALUES (?, ?, ?, ?, ?)
                """,
                (job_id, seq, event_type, payload_str, now),
            )
            return seq

    def get_job_events_after(self, job_id: str, after_seq: int = 0) -> List[Dict[str, Any]]:
        with self._lock, self._connect() as conn:
            rows = conn.execute(
                """
                SELECT seq, event_type, payload, created_at
                FROM job_events
                WHERE job_id = ? AND seq > ?
                ORDER BY seq ASC
                """,
                (job_id, after_seq),
            ).fetchall()
            result = []
            for r in rows:
                result.append(
                    {
                        "seq": r["seq"],
                        "event_type": r["event_type"],
                        "payload": json.loads(r["payload"]),
                        "created_at": r["created_at"],
                    }
                )
            return result

    # --- Local Ollama Conversations ---

    def ensure_local_conversation(self, conv_id: str, title: str, model: str) -> None:
        now = _utc_now()
        with self._lock, self._connect() as conn:
            existing = conn.execute(
                "SELECT id FROM local_conversations WHERE id = ?", (conv_id,)
            ).fetchone()
            if not existing:
                conn.execute(
                    """
                    INSERT INTO local_conversations (id, title, model, created_at, updated_at)
                    VALUES (?, ?, ?, ?, ?)
                    """,
                    (conv_id, title, model, now, now),
                )
            else:
                conn.execute(
                    "UPDATE local_conversations SET model = ?, updated_at = ? WHERE id = ?",
                    (model, now, conv_id),
                )

    def append_local_message(self, conv_id: str, role: str, content: str) -> None:
        now = _utc_now()
        with self._lock, self._connect() as conn:
            conn.execute(
                """
                INSERT INTO local_messages (conversation_id, role, content, created_at)
                VALUES (?, ?, ?, ?)
                """,
                (conv_id, role, content, now),
            )
            conn.execute(
                "UPDATE local_conversations SET updated_at = ? WHERE id = ?",
                (now, conv_id),
            )
            conv_row = conn.execute(
                "SELECT title FROM local_conversations WHERE id = ?", (conv_id,)
            ).fetchone()
            all_msgs = conn.execute(
                "SELECT content FROM local_messages WHERE conversation_id = ? ORDER BY id ASC",
                (conv_id,),
            ).fetchall()
            title = conv_row["title"] if conv_row else ""
            full_text = "\n".join(r["content"] for r in all_msgs)
            conn.execute("DELETE FROM conversations_fts WHERE conv_id = ?", (conv_id,))
            conn.execute(
                "INSERT INTO conversations_fts (conv_id, title, content) VALUES (?, ?, ?)",
                (conv_id, title, full_text),
            )

    def get_local_messages(self, conv_id: str) -> List[Dict[str, str]]:
        with self._lock, self._connect() as conn:
            rows = conn.execute(
                "SELECT role, content FROM local_messages WHERE conversation_id = ? ORDER BY id ASC",
                (conv_id,),
            ).fetchall()
            return [{"role": r["role"], "content": r["content"]} for r in rows]

    def get_local_conversation(self, conv_id: str) -> Optional[Dict[str, Any]]:
        with self._lock, self._connect() as conn:
            row = conn.execute(
                "SELECT * FROM local_conversations WHERE id = ?", (conv_id,)
            ).fetchone()
            if not row:
                return None
            msgs = conn.execute(
                "SELECT role, content, created_at FROM local_messages WHERE conversation_id = ? ORDER BY id ASC",
                (conv_id,),
            ).fetchall()
            conv = dict(row)
            conv["engine"] = "ollama"
            conv["messages"] = [
                {"role": "agent" if m["role"] == "assistant" else m["role"], "content": m["content"]}
                for m in msgs
            ]
            return conv

    def list_local_conversations(self) -> List[Dict[str, Any]]:
        with self._lock, self._connect() as conn:
            rows = conn.execute(
                """
                SELECT c.id, c.title, c.model, c.created_at, c.updated_at,
                       COUNT(m.id) AS message_count
                FROM local_conversations c
                LEFT JOIN local_messages m ON m.conversation_id = c.id
                GROUP BY c.id
                ORDER BY c.updated_at DESC
                """
            ).fetchall()
            return [
                {
                    "id": r["id"],
                    "title": r["title"],
                    "model": r["model"],
                    "engine": "ollama",
                    "created_at": r["created_at"],
                    "updated_at": r["updated_at"],
                    "message_count": r["message_count"],
                }
                for r in rows
            ]

    def delete_local_conversation(self, conv_id: str) -> bool:
        with self._lock, self._connect() as conn:
            conn.execute("DELETE FROM conversations_fts WHERE conv_id = ?", (conv_id,))
            cur = conn.execute("DELETE FROM local_conversations WHERE id = ?", (conv_id,))
            return cur.rowcount > 0

    # --- Settings ---

    def get_settings(self) -> Dict[str, Any]:
        defaults: Dict[str, Any] = {
            "default_permission_mode": "copilot",
            "default_model": "gemini-4-hydrogen-high",
            "default_effort": "",
        }
        with self._lock, self._connect() as conn:
            rows = conn.execute("SELECT key, value FROM settings").fetchall()
            for r in rows:
                try:
                    defaults[r["key"]] = json.loads(r["value"])
                except json.JSONDecodeError:
                    defaults[r["key"]] = r["value"]
        return defaults

    def update_settings(self, patch: Dict[str, Any]) -> Dict[str, Any]:
        now = _utc_now()
        with self._lock, self._connect() as conn:
            for k, v in patch.items():
                conn.execute(
                    """
                    INSERT INTO settings (key, value, updated_at)
                    VALUES (?, ?, ?)
                    ON CONFLICT(key) DO UPDATE SET value = excluded.value, updated_at = excluded.updated_at
                    """,
                    (k, json.dumps(v), now),
                )
        return self.get_settings()

    # --- FTS5 Search ---

    def upsert_fts(self, conv_id: str, title: str, content: str) -> None:
        with self._lock, self._connect() as conn:
            conn.execute("DELETE FROM conversations_fts WHERE conv_id = ?", (conv_id,))
            conn.execute(
                "INSERT INTO conversations_fts (conv_id, title, content) VALUES (?, ?, ?)",
                (conv_id, title, content),
            )

    def search_fts(self, query: str, limit: int = 40) -> List[str]:
        q = query.strip()
        if not q:
            return []
        # Escape FTS5 special characters and match prefix tokens
        tokens = [f'"{t.replace(chr(34), "")}"*' for t in q.split() if t.strip()]
        if not tokens:
            return []
        fts_query = " AND ".join(tokens)
        with self._lock, self._connect() as conn:
            rows = conn.execute(
                "SELECT conv_id FROM conversations_fts WHERE conversations_fts MATCH ? ORDER BY rank LIMIT ?",
                (fts_query, limit),
            ).fetchall()
            return [r["conv_id"] for r in rows]

    # --- Push Subscriptions ---

    def save_push_subscription(self, endpoint: str, sub_data: Dict[str, Any]) -> None:
        with self._lock, self._connect() as conn:
            conn.execute(
                """
                INSERT INTO push_subscriptions (endpoint, subscription_json, created_at)
                VALUES (?, ?, ?)
                ON CONFLICT(endpoint) DO UPDATE SET subscription_json = excluded.subscription_json
                """,
                (endpoint, json.dumps(sub_data), _utc_now()),
            )

    def delete_push_subscription(self, endpoint: str) -> None:
        with self._lock, self._connect() as conn:
            conn.execute("DELETE FROM push_subscriptions WHERE endpoint = ?", (endpoint,))

    def list_push_subscriptions(self) -> List[Dict[str, Any]]:
        with self._lock, self._connect() as conn:
            rows = conn.execute("SELECT subscription_json FROM push_subscriptions").fetchall()
            out = []
            for r in rows:
                try:
                    out.append(json.loads(r["subscription_json"]))
                except json.JSONDecodeError:
                    pass
            return out
