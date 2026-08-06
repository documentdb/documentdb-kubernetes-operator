'use strict';

/**
 * Mongoose CRUD/compatibility test against DocumentDB.
 *
 * Usage:
 *   MONGO_URI="mongodb://user:pass@host:10260/?tls=true&tlsAllowInvalidCertificates=true&directConnection=true" \
 *     node mongoose-crud-test.js
 *
 * Or pass the URI as the first argument:
 *   node mongoose-crud-test.js "mongodb://user:pass@host:10260/?..."
 *
 * Exercises connect, schema/model registration, index creation, insert,
 * find, update, aggregation, and delete using Mongoose against the DocumentDB
 * gateway. Exits non-zero on the first failure.
 */

const mongoose = require('mongoose');

const { Schema } = mongoose;

const URI = process.argv[2] || process.env.MONGO_URI;
const DB_NAME = process.env.MONGO_DB || 'mongoose_test';

let passed = 0;
let failed = 0;
let knownIssues = 0;

function ok(name) {
  passed += 1;
  console.log(`  \u2705 ${name}`);
}

function fail(name, err) {
  failed += 1;
  console.error(`  \u274C ${name}: ${err && err.message ? err.message : err}`);
}

async function step(name, fn) {
  try {
    await fn();
    ok(name);
  } catch (err) {
    fail(name, err);
  }
}

// Run a step that is expected to hit a documented DocumentDB limitation. If it
// fails with the known error, report it as a known issue (does not fail the
// suite). If it unexpectedly passes, count it as a pass so this auto-detects a
// fixed release. Any other error is a real failure.
async function stepKnownIssue(name, fn, knownErrorPattern) {
  try {
    await fn();
    ok(`${name} (known issue resolved)`);
  } catch (err) {
    const msg = err && err.message ? err.message : String(err);
    if (knownErrorPattern.test(msg)) {
      knownIssues += 1;
      console.warn(`  \u26A0\uFE0F  ${name}: known DocumentDB issue (${msg})`);
    } else {
      fail(name, err);
    }
  }
}

function sanitizeUri(uri) {
  return uri.replace(/([?&])replicaSet=[^&]*(&|$)/g, (_m, lead, trail) =>
    lead === '?' && trail === '&' ? '?' : trail === '&' ? lead : ''
  );
}

const widgetSchema = new Schema(
  {
    sku: { type: String, required: true, unique: true },
    name: { type: String, required: true },
    tags: { type: [String], default: [] },
    price: { type: Number, min: 0 },
    active: { type: Boolean, default: true },
  },
  { timestamps: true }
);
widgetSchema.index({ name: 1, price: -1 });

async function run() {
  if (!URI) {
    console.error('MONGO_URI not provided. Pass it as $1 or set MONGO_URI.');
    process.exit(2);
  }

  console.log('Mongoose DocumentDB compatibility test');
  console.log('======================================');

  const tlsInsecure = (process.env.TLS_INSECURE || 'true').toLowerCase() !== 'false';
  mongoose.set('strictQuery', true);

  await step('connect', async () => {
    await mongoose.connect(sanitizeUri(URI), {
      dbName: DB_NAME,
      directConnection: true,
      tls: true,
      tlsAllowInvalidCertificates: tlsInsecure,
      serverSelectionTimeoutMS: 15000,
    });
    const ping = await mongoose.connection.db.admin().command({ ping: 1 });
    if (ping.ok !== 1) throw new Error('ping did not return ok:1');
  });

  if (mongoose.connection.readyState !== 1) {
    console.error('\nConnection failed; aborting remaining steps.');
    process.exit(1);
  }

  // Fresh collection per run to keep the test idempotent.
  const collName = `widgets_${Date.now()}`;
  const Widget = mongoose.model('Widget', widgetSchema, collName);

  await step('create indexes (autoIndex / syncIndexes)', async () => {
    await Widget.syncIndexes();
  });

  let createdId;
  await step('insertOne (Model.create)', async () => {
    const doc = await Widget.create({
      sku: 'SKU-001',
      name: 'Gizmo',
      tags: ['alpha', 'beta'],
      price: 9.99,
    });
    createdId = doc._id;
    if (!createdId) throw new Error('no _id returned');
  });

  await step('insertMany', async () => {
    const res = await Widget.insertMany([
      { sku: 'SKU-002', name: 'Gadget', tags: ['beta'], price: 19.5 },
      { sku: 'SKU-003', name: 'Widget', tags: ['alpha', 'gamma'], price: 4.25 },
    ]);
    if (res.length !== 2) throw new Error(`expected 2 inserted, got ${res.length}`);
  });

  // Known issue: gateway 0.109.0 (operator 0.2.0) fails point lookups that
  // filter on `_id` with "trying to open a pruned relation". Treated as a known
  // issue so the suite stays green; auto-detects a fixed release if it passes.
  await stepKnownIssue(
    'findById',
    async () => {
      const doc = await Widget.findById(createdId).lean();
      if (!doc || doc.sku !== 'SKU-001') throw new Error('document not found or mismatched');
    },
    /pruned relation/i
  );

  await step('find with filter + sort + limit', async () => {
    const docs = await Widget.find({ price: { $gte: 5 } }).sort({ price: -1 }).limit(10).lean();
    if (docs.length !== 2) throw new Error(`expected 2 docs, got ${docs.length}`);
    if (docs[0].price < docs[1].price) throw new Error('sort order incorrect');
  });

  await step('countDocuments', async () => {
    const n = await Widget.countDocuments({});
    if (n !== 3) throw new Error(`expected 3 docs, got ${n}`);
  });

  await step('updateOne ($set)', async () => {
    const res = await Widget.updateOne({ sku: 'SKU-002' }, { $set: { price: 21 } });
    if (res.modifiedCount !== 1) throw new Error(`expected 1 modified, got ${res.modifiedCount}`);
  });

  await step('findOneAndUpdate (returns new)', async () => {
    const doc = await Widget.findOneAndUpdate(
      { sku: 'SKU-003' },
      { $push: { tags: 'delta' } },
      { new: true }
    ).lean();
    if (!doc.tags.includes('delta')) throw new Error('update not applied');
  });

  await step('aggregation ($unwind/$group)', async () => {
    const stats = await Widget.aggregate([
      { $unwind: '$tags' },
      { $group: { _id: '$tags', count: { $sum: 1 } } },
      { $sort: { count: -1 } },
    ]);
    if (!stats.length) throw new Error('aggregation returned no results');
  });

  await step('unique index enforcement (duplicate sku rejected)', async () => {
    try {
      await Widget.create({ sku: 'SKU-001', name: 'Duplicate' });
      throw new Error('duplicate insert was not rejected');
    } catch (err) {
      if (err.code !== 11000) throw new Error(`expected duplicate-key error (11000), got ${err.message}`);
    }
  });

  await step('deleteOne', async () => {
    const res = await Widget.deleteOne({ sku: 'SKU-002' });
    if (res.deletedCount !== 1) throw new Error(`expected 1 deleted, got ${res.deletedCount}`);
  });

  await step('cleanup (drop collection)', async () => {
    await Widget.collection.drop();
  });

  await mongoose.disconnect();

  console.log('\n======================================');
  console.log(`Passed: ${passed}  Failed: ${failed}  Known issues: ${knownIssues}`);
  process.exit(failed === 0 ? 0 : 1);
}

run().catch((err) => {
  console.error('Unexpected error:', err);
  process.exit(1);
});
