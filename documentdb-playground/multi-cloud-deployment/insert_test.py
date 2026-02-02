#!/usr/bin/env python3

import sys
import time
from pymongo import MongoClient, errors
from datetime import datetime

if len(sys.argv) != 2:
    print(f"Usage: python insert_test.py <connection_string>")
    sys.exit(1)

connection_string = sys.argv[1]

client = MongoClient(connection_string)

db = client.testdb
collection = db.testcollection

print(f"{'Inserted Document':<30} {'Insert Count':<15}")
print("-" * 77)
start_time = time.time()
end_time = start_time + (60 * 60)  # 60 minutes
count = 0
first_error_seen = False

while time.time() < end_time:
    write_result = ""
    try:
        doc = {
            "count": count,
            "message": f"Insert operation {count}"
        }
        result = collection.insert_one(doc)
        write_result = result.inserted_id
        count += 1
        print(f"{str(write_result):<30} {count:<15}")
    except Exception as e:
        if not first_error_seen:
            print("Switching cloud to Azure")
            first_error_seen = True
    
    time.sleep(.25)  

print(f"Completed {count} insert operations in 60 minutes")
final_read_count = collection.count_documents({})
print(f"Final read count: {final_read_count}")
client.close()
