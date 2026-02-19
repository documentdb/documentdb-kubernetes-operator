#!/usr/bin/env python3
"""
DocumentDB to Azure DocumentDB (with MongoDB compatibility) Change Stream Sync Service.

This service watches change streams from a DocumentDB instance (created by the
DocumentDB Kubernetes Operator) and replicates changes to Azure DocumentDB
(with MongoDB compatibility) at the collection level.

Features:
- Collection-level change stream (watches specific collections)
- Resume from last position after restarts using resume tokens
- Atomic state persistence for crash recovery
- Upsert-based sync for idempotency
- Automatic retry with exponential backoff

Usage:
    python sync.py [--config config.yaml] [--reset]

    --config: Path to configuration file (default: config.yaml)
    --reset: Reset sync state and start fresh
"""

import argparse
import hashlib
import logging
import os
import re
import signal
import sys
import time
from pathlib import Path
from typing import Dict, Any, Optional
from urllib.parse import urlparse

import yaml
from pymongo import MongoClient
from pymongo.errors import (
    ConnectionFailure,
    OperationFailure,
    ServerSelectionTimeoutError,
    PyMongoError
)

from state import SyncState

# Global flag for graceful shutdown
shutdown_requested = False


def setup_logging(config: Dict[str, Any]) -> None:
    """Configure logging based on config."""
    log_config = config.get("logging", {})
    logging.basicConfig(
        level=getattr(logging, log_config.get("level", "INFO")),
        format="%(asctime)s - %(levelname)s - %(message)s"
    )


def signal_handler(signum, frame):
    """Handle shutdown signals gracefully."""
    global shutdown_requested
    logging.info(f"Received signal {signum}, initiating graceful shutdown...")
    shutdown_requested = True


def load_config(config_path: str) -> Dict[str, Any]:
    """Load configuration from YAML file."""
    path = Path(config_path)
    if not path.exists():
        raise FileNotFoundError(f"Config file not found: {config_path}")
    
    with open(path) as f:
        return yaml.safe_load(f)


def extract_cluster_name(uri: str) -> str:
    """
    Extract cluster name from MongoDB connection URI.
    
    Examples:
        mongodb+srv://user:pass@my-cluster.mongocluster.cosmos.azure.com/...
        -> my-cluster
        
        mongodb://user:pass@localhost:27017/...
        -> localhost
    """
    try:
        # Handle mongodb+srv:// scheme
        if uri.startswith("mongodb+srv://"):
            # Extract host from URI
            match = re.search(r'@([^/\?]+)', uri)
            if match:
                host = match.group(1)
                # Get first part of hostname (cluster name)
                return host.split('.')[0]
        
        # Handle standard mongodb:// scheme
        parsed = urlparse(uri.replace("mongodb+srv://", "mongodb://"))
        if parsed.hostname:
            # For localhost or IP, use as-is
            if parsed.hostname in ('localhost', '127.0.0.1'):
                return f"{parsed.hostname}_{parsed.port or 27017}"
            # For other hosts, get cluster name (first part)
            return parsed.hostname.split('.')[0]
    except Exception:
        pass
    
    # Fallback: hash the URI
    return hashlib.md5(uri.encode()).hexdigest()[:12]


def generate_state_file_path(source_uri: str, target_uri: str) -> str:
    """
    Generate a unique state file path based on source and target cluster names.
    
    The state file is stored in the same directory as this script,
    ensuring it stays with the sync service project.
    """
    source_name = extract_cluster_name(source_uri)
    target_name = extract_cluster_name(target_uri)
    
    script_dir = os.path.dirname(os.path.abspath(__file__))
    return os.path.join(script_dir, f".documentdb_sync_state_{source_name}_to_{target_name}.json")


def connect_source(config: Dict[str, Any]) -> MongoClient:
    """Connect to source DocumentDB instance."""
    uri = config["source"]["uri"]
    logging.info("Connecting to source DocumentDB...")
    
    client = MongoClient(uri, serverSelectionTimeoutMS=10000)
    # Verify connection
    client.admin.command("ping")
    logging.info("Connected to source DocumentDB")
    return client


def connect_target(config: Dict[str, Any]) -> MongoClient:
    """Connect to target Azure DocumentDB (with MongoDB compatibility)."""
    uri = config["target"]["uri"]
    logging.info("Connecting to target Azure DocumentDB (with MongoDB compatibility)...")
    
    client = MongoClient(uri, serverSelectionTimeoutMS=30000)
    # Verify connection
    client.admin.command("ping")
    logging.info("Connected to target Azure DocumentDB (with MongoDB compatibility)")
    return client


def extract_document_id(document_key: Dict[str, Any], full_document: Optional[Dict[str, Any]] = None) -> Optional[Any]:
    """
    Extract the document _id from a change event's documentKey and/or fullDocument.

    Tries multiple strategies to handle different MongoDB-compatible implementations:
      1. documentKey["_id"]  — standard MongoDB format
      2. documentKey[""]     — local DocumentDB uses an empty-string key
      3. fullDocument["_id"] — fallback to the full document if available
      4. First value in documentKey — last-resort fallback

    Returns None if no ID can be determined.
    """
    doc_id = document_key.get("_id") or document_key.get("")
    if not doc_id and full_document:
        doc_id = full_document.get("_id")
    if not doc_id and document_key:
        doc_id = next(iter(document_key.values()), None)
        if doc_id:
            logging.debug(f"Extracted document ID via fallback (first value in documentKey): {doc_id}")
    return doc_id


def sync_change(
    change: Dict[str, Any],
    target_client: MongoClient,
    state: SyncState,
    config: Dict[str, Any]
) -> bool:
    """
    Sync a single change event to the target.
    
    Returns True if sync succeeded, False otherwise.
    """
    operation = change.get("operationType")
    ns = change.get("ns", {})
    db_name = ns.get("db")
    coll_name = ns.get("coll")
    document_key = change.get("documentKey", {})
    
    if not db_name or not coll_name:
        logging.warning(f"Invalid namespace in change event: {ns}")
        return True
    
    target_collection = target_client[db_name][coll_name]
    
    try:
        if operation == "insert":
            # Use upsert for idempotency (handles replays)
            full_document = change.get("fullDocument")
            if full_document:
                doc_to_write = {k: v for k, v in full_document.items() if k != "_id"}
                doc_id = extract_document_id(document_key, full_document)
                result = target_collection.replace_one(
                    {"_id": doc_id},
                    doc_to_write,
                    upsert=True
                )
                logging.debug(f"INSERT {db_name}.{coll_name}: _id={doc_id}")
                state.record_operation("insert")
            else:
                logging.warning(f"INSERT without fullDocument: {document_key}")
                
        elif operation in ("update", "replace"):
            # For updates, we need fullDocument (configured via fullDocument: updateLookup)
            full_document = change.get("fullDocument")
            if full_document:
                doc_to_write = {k: v for k, v in full_document.items() if k != "_id"}
                doc_id = extract_document_id(document_key, full_document)
                result = target_collection.replace_one(
                    {"_id": doc_id},
                    doc_to_write,
                    upsert=True
                )
                logging.debug(f"UPDATE {db_name}.{coll_name}: _id={doc_id}")
                state.record_operation("update")
            else:
                # Document was deleted before we could look it up
                logging.warning(f"UPDATE without fullDocument (doc may be deleted): {document_key}")
                
        elif operation == "delete":
            doc_id = extract_document_id(document_key) if document_key else None
            if doc_id:
                result = target_collection.delete_one({"_id": doc_id})
                logging.debug(f"DELETE {db_name}.{coll_name}: _id={doc_id}")
                state.record_operation("delete")
            else:
                logging.warning(f"DELETE without document key: {document_key}")
            
        elif operation == "drop":
            # Collection dropped
            logging.info(f"DROP collection: {db_name}.{coll_name}")
            try:
                target_client[db_name].drop_collection(coll_name)
            except OperationFailure as e:
                logging.warning(f"Could not drop collection on target: {e}")
                
        elif operation == "dropDatabase":
            # Database dropped
            logging.info(f"DROP database: {db_name}")
            try:
                target_client.drop_database(db_name)
            except OperationFailure as e:
                logging.warning(f"Could not drop database on target: {e}")
                
        elif operation == "invalidate":
            # Change stream invalidated (e.g., collection dropped)
            logging.warning("Change stream invalidated")
            return False
            
        else:
            logging.debug(f"Ignoring operation type: {operation}")
            
        return True
        
    except OperationFailure as e:
        logging.error(f"Operation failed for {operation} on {db_name}.{coll_name}: {e}")
        state.record_operation("error")
        # Don't fail the whole sync for individual doc failures
        return True
        
    except Exception as e:
        logging.error(f"Unexpected error syncing change: {e}")
        state.record_operation("error")
        return False


class MultiCollectionChangeStream:
    """
    Aggregates multiple collection-level change streams into a single iterator.
    
    Each stream is tagged with its collection name so resume tokens can be
    tracked per-collection.
    """
    
    def __init__(self, streams: list, collection_names: list):
        self.streams = streams
        self.collection_names = collection_names
        self.current_index = 0
    
    def try_next(self):
        """
        Try to get the next change from any of the watched collections.
        Uses round-robin to check each stream.
        
        Returns:
            Tuple of (collection_name, change_event) or (None, None) if no changes.
        """
        if not self.streams:
            return None, None
        
        # Check each stream once
        for _ in range(len(self.streams)):
            idx = self.current_index
            self.current_index = (self.current_index + 1) % len(self.streams)
            stream = self.streams[idx]
            coll_name = self.collection_names[idx]
            
            try:
                change = stream.try_next()
                if change is not None:
                    return coll_name, change
            except Exception as e:
                logging.warning(f"Error reading from stream {coll_name}: {e}")
        
        return None, None
    
    def close(self):
        """Close all underlying streams."""
        for stream in self.streams:
            try:
                stream.close()
            except Exception:
                pass


class SingleCollectionChangeStream:
    """
    Wraps a single collection change stream to match the MultiCollectionChangeStream
    interface (returns collection_name alongside the change).
    """
    
    def __init__(self, stream, collection_name: str):
        self.stream = stream
        self.collection_name = collection_name
    
    def try_next(self):
        try:
            change = self.stream.try_next()
            if change is not None:
                return self.collection_name, change
            return None, None
        except Exception as e:
            logging.warning(f"Error reading from stream {self.collection_name}: {e}")
            return None, None
    
    def close(self):
        try:
            self.stream.close()
        except Exception:
            pass


def open_change_stream(
    client: MongoClient,
    collections: list,
    pipeline: list,
    base_options: Dict[str, Any],
    state: SyncState
) -> Any:
    """
    Open collection-level change streams with per-collection resume tokens.
    
    Args:
        client: MongoDB client connection
        collections: List of "database.collection" strings to watch
        pipeline: Aggregation pipeline for filtering
        base_options: Change stream options (batch_size, max_await_time_ms, etc.)
        state: SyncState instance for reading per-collection resume tokens
    
    Returns:
        SingleCollectionChangeStream or MultiCollectionChangeStream
    """
    if not collections:
        raise ValueError("No collections specified to watch")
    
    logging.info(f"Opening collection-level change streams on {collections}...")
    streams = []
    stream_names = []
    for coll_spec in collections:
        try:
            # Parse "database.collection" format
            if "." in coll_spec:
                db_name, coll_name = coll_spec.split(".", 1)
            else:
                raise ValueError(f"Invalid collection spec '{coll_spec}', expected 'database.collection'")
            
            # Build per-collection options with its own resume token
            coll_options = dict(base_options)
            resume_token = state.get_resume_token(coll_spec)
            if resume_token:
                coll_options["resume_after"] = resume_token
                logging.info(f"  - Watching collection: {db_name}.{coll_name} (resuming)")
            else:
                logging.info(f"  - Watching collection: {db_name}.{coll_name} (from current position)")
            
            stream = client[db_name][coll_name].watch(pipeline, **coll_options)
            streams.append(stream)
            stream_names.append(coll_spec)
        except Exception as e:
            logging.error(f"  - Failed to watch collection {coll_spec}: {e}")
    
    if not streams:
        raise ValueError("Failed to open any collection change streams")
    
    if len(streams) == 1:
        return SingleCollectionChangeStream(streams[0], stream_names[0])
    
    return MultiCollectionChangeStream(streams, stream_names)


def run_sync(config: Dict[str, Any], state: SyncState) -> None:
    """Main sync loop."""
    global shutdown_requested
    
    source_client = None
    target_client = None
    
    try:
        source_client = connect_source(config)
        target_client = connect_target(config)
        
        persist_interval = config.get("state", {}).get("persist_interval", 10)
        
        # Build change stream options (without resume_after — that's per-collection)
        pipeline = []  # Empty pipeline = watch everything
        base_options = {
            "batch_size": 100,
            "max_await_time_ms": 5000,
            "full_document": "updateLookup",
        }
        
        # Get collections to watch from config
        collections = config.get("watch", {}).get("collections", [])
        
        # Initialize state for all collections (creates state file if needed)
        state.init_collections(collections)
        
        # Open collection-level change streams with per-collection resume tokens
        stream = open_change_stream(
            source_client, collections, pipeline, base_options, state
        )
        
        try:
            logging.info("Change stream opened successfully. Watching for changes...")
            
            changes_processed = 0
            last_log_time = time.time()
            
            while not shutdown_requested:
                try:
                    # Try to get next change (with timeout from max_await_time_ms)
                    coll_name, change = stream.try_next()
                    
                    if change is None:
                        # No changes available — flush any pending tokens to disk
                        state.flush_if_pending()
                        continue
                    
                    # Process the change
                    operation = change.get("operationType", "unknown")
                    ns = change.get("ns", {})
                    
                    logging.info(
                        f"Change: {operation} on {ns.get('db', '?')}.{ns.get('coll', '?')}"
                    )
                    
                    # Sync to target
                    success = sync_change(change, target_client, state, config)
                    
                    if not success:
                        logging.error("Failed to sync change, will retry on restart")
                        break
                    
                    # Update per-collection resume token
                    state.update_resume_token(coll_name, change["_id"], persist_interval)
                    changes_processed += 1
                    
                    # Periodic status log
                    if time.time() - last_log_time > 60:
                        stats = state.get_stats()
                        logging.info(
                            f"Sync status - Processed: {changes_processed}, "
                            f"Total: {stats.get('total_synced', 0)}, "
                            f"Errors: {stats.get('errors', 0)}"
                        )
                        last_log_time = time.time()
                        
                except StopIteration:
                    logging.warning("Change stream ended unexpectedly")
                    break
        finally:
            stream.close()
                    
    except ServerSelectionTimeoutError as e:
        logging.error(f"Could not connect to database: {e}")
        raise
        
    except ConnectionFailure as e:
        logging.error(f"Connection failed: {e}")
        raise
        
    except OperationFailure as e:
        if "not authorized" in str(e).lower():
            logging.error(f"Authentication/authorization error: {e}")
        elif "change stream" in str(e).lower():
            logging.error(f"Change stream error (is it enabled?): {e}")
        else:
            logging.error(f"Operation failed: {e}")
        raise
        
    finally:
        # Persist state before exit
        logging.info("Persisting final state...")
        state.persist()
        
        # Clean up connections
        if source_client:
            source_client.close()
        if target_client:
            target_client.close()
        
        stats = state.get_stats()
        logging.info(f"Final sync stats: {stats}")


def main():
    """Entry point."""
    parser = argparse.ArgumentParser(
        description="Sync DocumentDB changes to Azure DocumentDB (with MongoDB compatibility)"
    )
    parser.add_argument(
        "--config", "-c",
        default="config.yaml",
        help="Path to configuration file"
    )
    parser.add_argument(
        "--reset",
        action="store_true",
        help="Reset sync state and start fresh"
    )
    args = parser.parse_args()
    
    # Load configuration
    try:
        config = load_config(args.config)
    except FileNotFoundError as e:
        print(f"Error: {e}", file=sys.stderr)
        sys.exit(1)
    
    # Setup logging
    setup_logging(config)
    logger = logging.getLogger(__name__)
    
    logger.info("=" * 60)
    logger.info("DocumentDB to Azure DocumentDB (with MongoDB compatibility) Sync Service")
    logger.info("=" * 60)
    
    # Initialize state manager with auto-generated or configured path
    source_uri = config["source"]["uri"]
    target_uri = config["target"]["uri"]
    
    state_file = generate_state_file_path(source_uri, target_uri)
    logger.info(f"State file: {state_file}")
    
    source_name = extract_cluster_name(source_uri)
    target_name = extract_cluster_name(target_uri)
    logger.info(f"Syncing: {source_name} -> {target_name}")
    
    state = SyncState(state_file)
    
    if args.reset:
        logger.warning("Resetting sync state as requested")
        state.reset()
    
    # Setup signal handlers for graceful shutdown
    signal.signal(signal.SIGINT, signal_handler)
    signal.signal(signal.SIGTERM, signal_handler)
    
    # Run with automatic retry on transient failures
    max_retries = 5
    retry_delay = 5
    
    for attempt in range(max_retries):
        try:
            run_sync(config, state)
            
            if shutdown_requested:
                logger.info("Graceful shutdown completed")
                break
                
        except (ConnectionFailure, ServerSelectionTimeoutError) as e:
            if attempt < max_retries - 1:
                logger.warning(
                    f"Connection error (attempt {attempt + 1}/{max_retries}), "
                    f"retrying in {retry_delay}s: {e}"
                )
                time.sleep(retry_delay)
                retry_delay = min(retry_delay * 2, 60)  # Exponential backoff, max 60s
            else:
                logger.error(f"Max retries exceeded: {e}")
                sys.exit(1)
                
        except KeyboardInterrupt:
            logger.info("Interrupted by user")
            break
            
        except Exception as e:
            logger.exception(f"Unexpected error: {e}")
            sys.exit(1)
    
    logger.info("Sync service stopped")


if __name__ == "__main__":
    main()
