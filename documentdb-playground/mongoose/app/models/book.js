'use strict';

const mongoose = require('mongoose');

const { Schema } = mongoose;

const bookSchema = new Schema(
  {
    title: { type: String, required: true, trim: true },
    author: { type: String, required: true },
    genres: { type: [String], default: [] },
    pages: { type: Number, min: 1 },
    published: { type: Date },
    inStock: { type: Boolean, default: true },
    rating: { type: Number, min: 0, max: 5 },
  },
  { timestamps: true }
);

// Compound index: exercises DocumentDB index creation via Mongoose autoIndex.
// Note: no `collation` option here; DocumentDB does not implement collation indexes.
bookSchema.index({ author: 1, title: 1 });

module.exports = mongoose.model('Book', bookSchema);
