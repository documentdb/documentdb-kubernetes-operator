"""
Resume token state management for DocumentDB change stream sync.

Provides crash-safe persistence of resume tokens to enable catching up
after program restarts or failures.
"""

import json
import os
import tempfile
import logging
from pathlib import Path
from typing import Optional, Dict, Any
from datetime import datetime

logger = logging.getLogger(__name__)


class SyncState:
    """
    Manages persistent state for change stream synchronization.
    
    Stores per-collection resume tokens and sync metadata in a JSON file
    with atomic writes to prevent corruption on crashes.
    
    State file format:
    {
        "resume_tokens": {
            "cstest.items": { "_data": "hex_string" },
            "cstest.orders": { "_data": "hex_string" }
        },
        "last_sync_time": "2026-02-05T10:30:00Z",
        "sync_stats": {
            "total_synced": 12345,
            "inserts": 5000,
            "updates": 4000,
            "deletes": 3345
        }
    }
    """
    
    def __init__(self, state_file: str):
        """
        Initialize state manager.
        
        Args:
            state_file: Path to state file (supports ~ expansion)
        """
        self.state_file = Path(state_file).expanduser()
        self._state: Dict[str, Any] = {
            "resume_tokens": {},
            "last_sync_time": None,
            "sync_stats": {
                "total_synced": 0,
                "inserts": 0,
                "updates": 0,
                "deletes": 0,
                "errors": 0
            }
        }
        self._changes_since_persist = 0
        self._load()
    
    def _load(self) -> None:
        """Load state from file if it exists."""
        if self.state_file.exists():
            try:
                with open(self.state_file, 'r') as f:
                    loaded = json.load(f)
                    self._state.update(loaded)
                logger.info(f"Loaded sync state from {self.state_file}")
                tokens = self._state.get("resume_tokens", {})
                if tokens:
                    for coll, token in tokens.items():
                        token_preview = token.get('_data', '')[:20] if isinstance(token, dict) else str(token)[:20]
                        logger.info(f"  Will resume {coll} from token: {token_preview}...")
                    stats = self._state.get("sync_stats", {})
                    logger.info(f"Previous sync stats - Total: {stats.get('total_synced', 0)}, "
                               f"Inserts: {stats.get('inserts', 0)}, "
                               f"Updates: {stats.get('updates', 0)}, "
                               f"Deletes: {stats.get('deletes', 0)}")
            except json.JSONDecodeError as e:
                logger.warning(f"Corrupted state file, starting fresh: {e}")
            except Exception as e:
                logger.warning(f"Could not load state file: {e}")
        else:
            logger.info(f"No existing state file at {self.state_file}, starting fresh sync")
    
    def _save(self) -> None:
        """
        Atomically save state to file.
        
        Uses write-to-temp-then-rename pattern for crash safety.
        """
        # Ensure parent directory exists
        self.state_file.parent.mkdir(parents=True, exist_ok=True)
        
        # Write to temp file first
        fd, temp_path = tempfile.mkstemp(
            dir=self.state_file.parent,
            prefix='.sync_state_',
            suffix='.tmp'
        )
        try:
            with os.fdopen(fd, 'w') as f:
                json.dump(self._state, f, indent=2, default=str)
            
            # Atomic replace (works on both Windows and POSIX)
            os.replace(temp_path, self.state_file)
            logger.debug(f"State persisted to {self.state_file}")
        except Exception as e:
            # Clean up temp file on failure
            try:
                os.unlink(temp_path)
            except OSError:
                pass
            raise e
    
    def init_collections(self, collections: list) -> None:
        """
        Initialize resume tokens for the given collections.
        
        Ensures every watched collection has an entry in resume_tokens
        (set to None if not already present). Persists the state file
        immediately so it exists before consuming any changes.
        
        Args:
            collections: List of "database.collection" strings to watch
        """
        tokens = self._state.setdefault("resume_tokens", {})
        for coll in collections:
            if coll not in tokens:
                tokens[coll] = None
                logger.info(f"  Initialized resume token for {coll} (fresh start)")
            else:
                token = tokens[coll]
                if token:
                    token_preview = token.get('_data', '')[:20] if isinstance(token, dict) else str(token)[:20]
                    logger.info(f"  Existing resume token for {coll}: {token_preview}...")
                else:
                    logger.info(f"  No resume token for {coll} (fresh start)")
        # Persist immediately so state file exists before consuming changes
        self.persist()
        logger.info(f"State file initialized with {len(collections)} collection(s)")
    
    def get_resume_token(self, collection: str) -> Optional[Dict[str, Any]]:
        """
        Get the stored resume token for a specific collection.
        
        Args:
            collection: The "database.collection" string
            
        Returns:
            Resume token dict, or None if starting fresh.
        """
        return self._state.get("resume_tokens", {}).get(collection)
    
    def update_resume_token(self, collection: str, token: Dict[str, Any], persist_interval: int = 10) -> None:
        """
        Update the resume token for a specific collection.
        
        Args:
            collection: The "database.collection" string
            token: The resume token from the change event (_id field)
            persist_interval: Persist to disk every N changes
        """
        self._state.setdefault("resume_tokens", {})[collection] = token
        self._state["last_sync_time"] = datetime.utcnow().isoformat() + "Z"
        self._changes_since_persist += 1
        
        # Periodic persistence to balance durability and performance
        if self._changes_since_persist >= persist_interval:
            self.persist()
            self._changes_since_persist = 0
    
    def record_operation(self, operation_type: str) -> None:
        """
        Record a sync operation in stats.
        
        Args:
            operation_type: One of 'insert', 'update', 'replace', 'delete', 'error'
        """
        stats = self._state["sync_stats"]
        stats["total_synced"] = stats.get("total_synced", 0) + 1
        
        if operation_type in ("insert",):
            stats["inserts"] = stats.get("inserts", 0) + 1
        elif operation_type in ("update", "replace"):
            stats["updates"] = stats.get("updates", 0) + 1
        elif operation_type == "delete":
            stats["deletes"] = stats.get("deletes", 0) + 1
        elif operation_type == "error":
            stats["errors"] = stats.get("errors", 0) + 1
    
    def persist(self) -> None:
        """Force immediate persistence of state to disk."""
        self._save()
        self._changes_since_persist = 0
        logger.debug("State persisted to disk")
    
    def flush_if_pending(self) -> None:
        """
        Persist state to disk if there are unpersisted changes.
        
        Call this when the change stream is idle to ensure tokens are
        flushed even when the number of changes is less than persist_interval.
        """
        if self._changes_since_persist > 0:
            logger.info(f"Persisting resume tokens to state file for {self._changes_since_persist} change(s)")
            self.persist()
    
    def get_stats(self) -> Dict[str, int]:
        """Get current sync statistics."""
        return self._state.get("sync_stats", {}).copy()
    
    def reset(self) -> None:
        """Reset state to start fresh (use with caution)."""
        self._state = {
            "resume_tokens": {},
            "last_sync_time": None,
            "sync_stats": {
                "total_synced": 0,
                "inserts": 0,
                "updates": 0,
                "deletes": 0,
                "errors": 0
            }
        }
        self._changes_since_persist = 0
        if self.state_file.exists():
            self.state_file.unlink()
            logger.info("State reset - removed state file")
