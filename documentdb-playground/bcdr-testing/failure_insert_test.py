#!/usr/bin/env python3

import argparse
import sys
import time
import uuid
from datetime import datetime, timezone

from pymongo import MongoClient
from pymongo.errors import PyMongoError


def build_connection_string(host: str, username: str, password: str, port: int, use_srv=False) -> str:
    if use_srv:
        return (
            f"mongodb+srv://{username}:{password}@{host}/"
            "?authMechanism=SCRAM-SHA-256&tls=true&tlsAllowInvalidCertificates=true"
        )
    else:
        return (
            f"mongodb://{username}:{password}@{host}:{port}/"
            "?authMechanism=SCRAM-SHA-256&tls=true&tlsAllowInvalidCertificates=true"
        )


def parse_args() -> argparse.Namespace:
    parser = argparse.ArgumentParser(description="failure insert/read test")
    parser.add_argument("insert_host")
    parser.add_argument("read_host_1")
    parser.add_argument("read_host_2")
    parser.add_argument("username")
    parser.add_argument("password")
    parser.add_argument("--use-srv", type=bool, default=False, help="Use srv connection string")
    parser.add_argument("--duration-seconds", type=int, default=600)
    parser.add_argument("--sleep-seconds", type=float, default=0.2)
    parser.add_argument("--port", type=int, default=10260)
    return parser.parse_args()


def main() -> int:
    args = parse_args()

    insert_client = MongoClient(
        build_connection_string(args.insert_host, args.username, args.password, args.port, args.use_srv)
    )
    read_client_1 = MongoClient(
        build_connection_string(args.read_host_1, args.username, args.password, args.port)
    )
    read_client_2 = MongoClient(
        build_connection_string(args.read_host_2, args.username, args.password, args.port)
    )

    collection_name = f"testcollection_{uuid.uuid4().hex}"
    insert_collection = insert_client.testdb[collection_name]
    read_collection_1 = read_client_1.testdb[collection_name]
    read_collection_2 = read_client_2.testdb[collection_name]

    print(f"Using collection: {collection_name}")

    start_time = time.time()
    end_time = start_time + args.duration_seconds

    successful_inserts = 0
    last_success_time = None
    last_success_before_failure = None
    first_failure_time = None
    recovery_time = None

    print(f"{'Inserted Document':<30} {'Insert Count':<15} {'Read1 Count':<15} {'Read2 Count':<15}")
    print("-" * 85)

    while time.time() < end_time:
        try:
            now = datetime.now(timezone.utc)
            doc = {
                "count": successful_inserts,
                "message": f"Insert operation {successful_inserts}",
                "timestamp": now,
            }
            result = insert_collection.insert_one(doc)
            successful_inserts += 1
            last_success_time = time.time()
            if first_failure_time is not None and recovery_time is None:
                recovery_time = last_success_time

            try:
                read_count_1 = read_collection_1.count_documents({})
                read_count_2 = read_collection_2.count_documents({})
            except PyMongoError as exc:
                print(f"Read error: {exc}")
                read_count_1 = -1
                read_count_2 = -1

            print(
                f"{str(result.inserted_id):<30} {successful_inserts:<15} {read_count_1:<15} {read_count_2:<15}"
            )
        except PyMongoError as exc:
            if first_failure_time is None:
                first_failure_time = time.time()
                last_success_before_failure = last_success_time
            print(f"Mongo error: {exc}")
        except Exception as exc:
            if first_failure_time is None:
                first_failure_time = time.time()
                last_success_before_failure = last_success_time
            print(f"Unexpected error: {exc}")

        time.sleep(args.sleep_seconds)

    final_read_count_1 = read_collection_1.count_documents({})
    final_read_count_2 = read_collection_2.count_documents({})

    print(f"Completed {successful_inserts} insert operations")
    print(f"Final read count (read_host_1): {final_read_count_1}")
    print(f"Final read count (read_host_2): {final_read_count_2}")

    data_loss_1 = max(0, successful_inserts - final_read_count_1)
    data_loss_2 = max(0, successful_inserts - final_read_count_2)
    print(f"Data loss (read_host_1): {data_loss_1}")
    print(f"Data loss (read_host_2): {data_loss_2}")

    if first_failure_time is not None and recovery_time is not None and last_success_before_failure is not None:
        downtime = recovery_time - last_success_before_failure
        print(f"Downtime (s): {downtime:.2f}")
    else:
        print("Downtime (s): N/A")

    insert_client.close()
    read_client_1.close()
    read_client_2.close()

    return 0


if __name__ == "__main__":
    sys.exit(main())
