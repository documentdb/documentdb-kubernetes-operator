#!/usr/bin/env python3

import sys
import time
import uuid
from datetime import datetime, timezone
from pymongo import MongoClient
from pymongo.errors import PyMongoError


def build_connection_string(ip: str, password: str, port: int = 10260) -> str:
    return f"mongodb://default_user:{password}@{ip}:{port}/?authMechanism=SCRAM-SHA-256&tls=true&tlsAllowInvalidCertificates=true"


def main() -> int:
    if len(sys.argv) != 5:
        print("Usage: python insert_read_test.py <insert_ip> <read_ip_1> <read_ip_2> <password>")
        return 1

    insert_ip, read_ip_1, read_ip_2, password = sys.argv[1:5]

    insert_client = MongoClient(build_connection_string(insert_ip, password))
    read_client_1 = MongoClient(build_connection_string(read_ip_1, password))
    read_client_2 = MongoClient(build_connection_string(read_ip_2, password))

    collection_name = f"testcollection_{uuid.uuid4().hex}"
    insert_collection = insert_client.testdb[collection_name]
    read_collection_1 = read_client_1.testdb[collection_name]
    read_collection_2 = read_client_2.testdb[collection_name]

    print(f"Using collection: {collection_name}")

    print(f"{'Inserted Document':<30} {'Insert Count':<15} {'Read1 Count':<15} {'Read2 Count':<15}")
    print("-" * 85)

    start_time = time.time()
    end_time = start_time + (10 * 60)  # 10 minutes
    count = 0

    while time.time() < end_time:
        try:
            doc = {
                "count": count,
                "message": f"Insert operation {count}",
                "timestamp": datetime.now(timezone.utc),
            }
            result = insert_collection.insert_one(doc)
            count += 1

            read_count_1 = read_collection_1.count_documents({})
            read_count_2 = read_collection_2.count_documents({})

            print(f"{str(result.inserted_id):<30} {count:<15} {read_count_1:<15} {read_count_2:<15}")
        except PyMongoError as exc:
            print(f"Mongo error: {exc}")
        except Exception as exc:
            print(f"Unexpected error: {exc}")

        time.sleep(1)

    print(f"Completed {count} insert operations in 10 minutes")
    final_read_count_1 = read_collection_1.count_documents({})
    final_read_count_2 = read_collection_2.count_documents({})
    print(f"Final read count (read_ip_1): {final_read_count_1}")
    print(f"Final read count (read_ip_2): {final_read_count_2}")

    insert_client.close()
    read_client_1.close()
    read_client_2.close()

    return 0


if __name__ == "__main__":
    raise SystemExit(main())
