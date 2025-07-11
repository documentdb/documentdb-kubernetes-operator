#!/usr/bin/env python3

import sys
import time
from pymongo import MongoClient, errors
from datetime import datetime

if len(sys.argv) != 2:
    print("Usage: python insert_test.py <connection_string>")
    sys.exit(1)

connection_string = sys.argv[1]

client = MongoClient(connection_string)

db = client.testdb
collection = db.testcollection

# Perform single insert operation
print(f"Performing initial insert operation...")
print(f"Using: {connection_string.split('@')[1] if '@' in connection_string else 'local'}")
print()
print(f"Starting read operations for 10 minutes...")
print(f"{'Timestamp':<20} {'Read Count':<15} {'Status':<20}")
print("-" * 80)

start_time = time.time()
end_time = start_time + (10 * 60)  # 10 minutes
read_count = 0
error_count = 0

while time.time() < end_time:
    timestamp = datetime.now().strftime("%H:%M:%S")
    try:
        count = collection.count_documents({})
        read_count += 1
        print(f"{timestamp:<20} {count:<15} {'Success':<20}")
    except Exception as e:
        error_count += 1
        print(f"{timestamp:<20} {'N/A':<15} {'ERROR':<20}")
        print(f"  Exception Type: {type(e).__name__}")
        print(f"  Exception Message: {str(e)}")
        if hasattr(e, 'details'):
            print(f"  Details: {e.details}")
        if hasattr(e, '__cause__'):
            print(f"  Cause: {e.__cause__}")
        print()
    
    time.sleep(1)  

print()
print(f"Completed {read_count} successful read operations in 10 minutes")
print(f"Total errors: {error_count}")
try:
    final_count = collection.count_documents({})
    print(f"Final document count: {final_count}")
except Exception as e:
    print(f"ERROR reading final count:")
    print(f"  Exception Type: {type(e).__name__}")
    print(f"  Exception Message: {str(e)}")
client.close()
