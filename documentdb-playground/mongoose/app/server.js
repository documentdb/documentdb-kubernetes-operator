'use strict';

const express = require('express');
const mongoose = require('mongoose');

const { connect } = require('./db');
const Book = require('./models/book');

const app = express();
app.use(express.json());

const PORT = Number(process.env.PORT || 3000);

// Liveness/readiness probe. Returns 200 only when the Mongoose connection is up.
app.get('/health', (req, res) => {
  const state = mongoose.connection.readyState; // 1 = connected
  if (state === 1) {
    return res.json({ status: 'healthy', db: 'connected' });
  }
  return res.status(503).json({ status: 'unhealthy', db: mongoose.STATES[state] });
});

// Create a book.
app.post('/books', async (req, res) => {
  try {
    const book = await Book.create(req.body);
    res.status(201).json(book);
  } catch (err) {
    res.status(400).json({ error: err.message });
  }
});

// List books, optionally filtered by author.
app.get('/books', async (req, res) => {
  try {
    const filter = req.query.author ? { author: req.query.author } : {};
    const books = await Book.find(filter).sort({ createdAt: -1 }).limit(100).lean();
    res.json({ count: books.length, books });
  } catch (err) {
    res.status(500).json({ error: err.message });
  }
});

// Fetch a single book by id.
app.get('/books/:id', async (req, res) => {
  try {
    const book = await Book.findById(req.params.id).lean();
    if (!book) return res.status(404).json({ error: 'not found' });
    res.json(book);
  } catch (err) {
    res.status(400).json({ error: err.message });
  }
});

// Update a book.
app.patch('/books/:id', async (req, res) => {
  try {
    const book = await Book.findByIdAndUpdate(req.params.id, req.body, {
      new: true,
      runValidators: true,
    }).lean();
    if (!book) return res.status(404).json({ error: 'not found' });
    res.json(book);
  } catch (err) {
    res.status(400).json({ error: err.message });
  }
});

// Delete a book.
app.delete('/books/:id', async (req, res) => {
  try {
    const result = await Book.findByIdAndDelete(req.params.id).lean();
    if (!result) return res.status(404).json({ error: 'not found' });
    res.status(204).end();
  } catch (err) {
    res.status(400).json({ error: err.message });
  }
});

// Simple aggregation: count books per genre.
app.get('/stats/genres', async (req, res) => {
  try {
    const stats = await Book.aggregate([
      { $unwind: '$genres' },
      { $group: { _id: '$genres', count: { $sum: 1 } } },
      { $sort: { count: -1 } },
    ]);
    res.json(stats);
  } catch (err) {
    res.status(500).json({ error: err.message });
  }
});

async function main() {
  await connect();
  console.log('Connected to DocumentDB via Mongoose');
  const server = app.listen(PORT, () =>
    console.log(`Mongoose demo API listening on :${PORT}`)
  );

  const shutdown = (signal) => {
    console.log(`Received ${signal}, shutting down...`);
    server.close(() => {
      mongoose.connection.close(false).finally(() => process.exit(0));
    });
  };
  ['SIGTERM', 'SIGINT'].forEach((sig) => process.on(sig, () => shutdown(sig)));
}

main().catch((err) => {
  console.error('Fatal startup error:', err.message);
  process.exit(1);
});
