#!/usr/bin/env python3
"""Small end-to-end demo: the classic DynamoDB "Movies" table, run against
ExtendDB (which persists the data in DocumentDB).

Usage:
    pip install -r requirements.txt
    kubectl port-forward svc/extenddb 18443:18443 -n extenddb &
    EXTENDDB_ACCESS_KEY_ID=<...> EXTENDDB_SECRET_ACCESS_KEY=<...> ./demo.py

Environment variables:
    EXTENDDB_ENDPOINT            default: https://127.0.0.1:18443
    EXTENDDB_ACCESS_KEY_ID        required -- a SigV4 access key, NOT the
                                  admin username/password `extenddb init`
                                  prints (that's for the management API
                                  only). See ../README.md's "Creating a
                                  DynamoDB API access key" section for the
                                  extenddb manage steps to create one.
    EXTENDDB_SECRET_ACCESS_KEY    required (secret half of the key above)
    AWS_DEFAULT_REGION            default: us-east-1

ExtendDB uses a self-signed TLS certificate by default; this demo disables
certificate verification for simplicity (fine for a local playground, not
for anything you'd point at a real deployment).

The playground Deployment configures majority transaction read concern for
DocumentDB compatibility; see ../README.md for the isolation tradeoff.
"""

import os
import sys
from decimal import Decimal

import boto3
import urllib3
from boto3.dynamodb.conditions import Key
from botocore.exceptions import ClientError

# Silence the "InsecureRequestWarning" noise from disabling TLS verification
# against ExtendDB's self-signed certificate.
urllib3.disable_warnings(urllib3.exceptions.InsecureRequestWarning)

TABLE_NAME = "Movies"

MOVIES = [
    {"year": 2013, "title": "Rush", "info": {"rating": Decimal("8.1"), "genres": ["Action", "Biography", "Drama"]}},
    {"year": 2013, "title": "Prisoners", "info": {"rating": Decimal("8.2"), "genres": ["Crime", "Drama", "Mystery"]}},
    {"year": 2014, "title": "Interstellar", "info": {"rating": Decimal("8.6"), "genres": ["Adventure", "Drama", "Sci-Fi"]}},
]


def get_dynamodb_resource():
    endpoint = os.environ.get("EXTENDDB_ENDPOINT", "https://127.0.0.1:18443")
    access_key = os.environ.get("EXTENDDB_ACCESS_KEY_ID")
    secret_key = os.environ.get("EXTENDDB_SECRET_ACCESS_KEY")
    region = os.environ.get("AWS_DEFAULT_REGION", "us-east-1")

    if not access_key or not secret_key:
        sys.exit(
            "Set EXTENDDB_ACCESS_KEY_ID and EXTENDDB_SECRET_ACCESS_KEY "
            "(printed by ../scripts/deploy.sh after 'extenddb init')."
        )

    session = boto3.session.Session(
        aws_access_key_id=access_key,
        aws_secret_access_key=secret_key,
        region_name=region,
    )
    return session.resource("dynamodb", endpoint_url=endpoint, verify=False)


def create_table(dynamodb):
    print(f"--- Creating table '{TABLE_NAME}' ---")
    try:
        table = dynamodb.create_table(
            TableName=TABLE_NAME,
            KeySchema=[
                {"AttributeName": "year", "KeyType": "HASH"},
                {"AttributeName": "title", "KeyType": "RANGE"},
            ],
            AttributeDefinitions=[
                {"AttributeName": "year", "AttributeType": "N"},
                {"AttributeName": "title", "AttributeType": "S"},
            ],
            BillingMode="PAY_PER_REQUEST",
        )
        table.wait_until_exists()
    except ClientError as e:
        if e.response["Error"]["Code"] == "ResourceInUseException":
            print(f"Table '{TABLE_NAME}' already exists, reusing it.")
            table = dynamodb.Table(TABLE_NAME)
        else:
            raise
    return table


def load_movies(table):
    print(f"--- Loading {len(MOVIES)} sample items ---")
    with table.batch_writer() as batch:
        for movie in MOVIES:
            batch.put_item(Item=movie)


def get_movie(table, year, title):
    print(f"--- GetItem: {title} ({year}) ---")
    resp = table.get_item(Key={"year": year, "title": title})
    print(resp.get("Item"))


def query_by_year(table, year):
    print(f"--- Query: all movies from {year} ---")
    resp = table.query(KeyConditionExpression=Key("year").eq(year))
    for item in resp["Items"]:
        print(f"  {item['title']}: rating {item['info']['rating']}")


def update_rating(table, year, title, new_rating):
    print(f"--- UpdateItem: bump {title} rating to {new_rating} ---")
    table.update_item(
        Key={"year": year, "title": title},
        UpdateExpression="SET info.rating = :r",
        ExpressionAttributeValues={":r": new_rating},
    )


def scan_all(table):
    print("--- Scan: entire table ---")
    resp = table.scan()
    for item in resp["Items"]:
        print(f"  {item['year']} - {item['title']}")


def delete_movie(table, year, title):
    print(f"--- DeleteItem: {title} ({year}) ---")
    table.delete_item(Key={"year": year, "title": title})


def delete_table(table):
    print(f"--- Deleting table '{TABLE_NAME}' ---")
    table.delete()
    table.wait_until_not_exists()


def main():
    dynamodb = get_dynamodb_resource()
    table = create_table(dynamodb)
    load_movies(table)

    get_movie(table, 2014, "Interstellar")
    query_by_year(table, 2013)
    update_rating(table, 2013, "Rush", Decimal("8.3"))
    get_movie(table, 2013, "Rush")
    scan_all(table)
    delete_movie(table, 2013, "Prisoners")
    scan_all(table)
    delete_table(table)

    print("\n✓ Demo complete -- DynamoDB API calls were served by ExtendDB, backed by DocumentDB.")


if __name__ == "__main__":
    main()
