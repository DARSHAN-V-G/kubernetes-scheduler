const express = require('express');
const { MongoClient } = require('mongodb');
const { createClient } = require('redis');
const { logger, morganMiddleware } = require('./logger');

const app = express();
const PORT = process.env.PORT || 3000;

app.use(express.json());
app.use(morganMiddleware);

// Config URLs
const mongoUrl = process.env.MONGO_URI || 'mongodb://admin:mongo-secure-password@mongodb:27017';
const redisUrl = process.env.REDIS_URL || 'redis://redis:6379';

const seedProducts = [
  { id: 'p1', name: 'Premium Cloud Arch Shirt', price: 29.99, category: 'Apparel', description: 'Over-provisioned comfort' },
  { id: 'p2', name: 'Kubernetes Scheduler Mug', price: 14.99, category: 'Kitchenware', description: 'Keeps beverages warm while pods are pending' },
  { id: 'p3', name: 'Node Allocator Plushie', price: 24.99, category: 'Toys', description: 'Reclaims logical resources on contact' },
  { id: 'p4', name: 'Autoscaler Keycap', price: 9.99, category: 'Electronics', description: 'Fills node capacity with a single click' }
];

let db;
let productsCollection;
let redisClient;

// Retry connecting to MongoDB
const connectMongo = async (retries = 5, delay = 5000) => {
  const client = new MongoClient(mongoUrl, { serverSelectionTimeoutMS: 3000 });
  while (retries > 0) {
    try {
      await client.connect();
      db = client.db('ecommerce');
      productsCollection = db.collection('products');
      logger.info('Connected to MongoDB successfully.');
      
      // Seed database if empty
      const count = await productsCollection.countDocuments();
      if (count === 0) {
        await productsCollection.insertMany(seedProducts);
        logger.info('Seeded default product database catalogs.');
      }
      break;
    } catch (err) {
      retries -= 1;
      logger.warn(`MongoDB connection failed. Retrying in ${delay / 1000}s. Errors: ${err.message}`);
      await new Promise(res => setTimeout(res, delay));
    }
  }
  if (retries === 0) {
    logger.error('Failed to connect to MongoDB. Exiting.');
    process.exit(1);
  }
};

// Retry connecting to Redis
const connectRedis = async (retries = 5, delay = 5000) => {
  redisClient = createClient({ url: redisUrl });
  redisClient.on('error', (err) => logger.error(`Redis Client Error: ${err.message}`));
  
  while (retries > 0) {
    try {
      await redisClient.connect();
      logger.info('Connected to Redis Cache successfully.');
      break;
    } catch (err) {
      retries -= 1;
      logger.warn(`Redis connection failed. Retrying in ${delay / 1000}s...`);
      await new Promise(res => setTimeout(res, delay));
    }
  }
  if (retries === 0) {
    logger.error('Failed to connect to Redis. Exiting.');
    process.exit(1);
  }
};

const initConnections = async () => {
  await connectMongo();
  await connectRedis();
};

initConnections();

// Service Health Check
app.get('/api/products/health', (req, res) => {
  res.json({ status: 'UP', service: 'product-service' });
});

// GET Catalog List (w/ Redis caching layer)
app.get('/api/products', async (req, res) => {
  try {
    // Check Cache
    const cachedProducts = await redisClient.get('products_list');
    if (cachedProducts) {
      logger.info('Serving product listings from Redis Cache hit.');
      return res.json(JSON.parse(cachedProducts));
    }

    // Cache Miss, fetch from MongoDB
    const products = await productsCollection.find({}).toArray();
    
    // Store in cache for 60 seconds
    await redisClient.setEx('products_list', 60, JSON.stringify(products));
    logger.info('Serving product listings from MongoDB. Cache updated.');
    res.json(products);
  } catch (err) {
    logger.error(`Error fetching products: ${err.message}`);
    res.status(500).json({ message: 'Internal Server Error' });
  }
});

// Search Catalog (directly from MongoDB)
app.get('/api/products/search', async (req, res) => {
  const query = req.query.q || '';
  try {
    const products = await productsCollection.find({
      $or: [
        { name: { $regex: query, $options: 'i' } },
        { category: { $regex: query, $options: 'i' } },
        { description: { $regex: query, $options: 'i' } }
      ]
    }).toArray();
    res.json(products);
  } catch (err) {
    logger.error(`Search error: ${err.message}`);
    res.status(500).json({ message: 'Internal Server Error' });
  }
});

app.listen(PORT, () => {
  logger.info(`Product Service listening on port ${PORT}`);
});
