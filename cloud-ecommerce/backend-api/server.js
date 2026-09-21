const express = require('express');
const cors = require('cors');
const { Pool } = require('pg');
const { createClient } = require('redis');
const bcrypt = require('bcryptjs');
const jwt = require('jsonwebtoken');
const { logger, morganMiddleware } = require('./logger');

const app = express();
const PORT = process.env.PORT || 3000;
const JWT_SECRET = process.env.JWT_SECRET || 'super-secret-key';
const REDIS_URL = process.env.REDIS_URL || 'redis://redis:6379';

app.use(cors());
app.use(express.json());
app.use(morganMiddleware);

// PostgreSQL Connection Pool
const pool = new Pool({
  host: process.env.DB_HOST || 'postgres',
  port: parseInt(process.env.DB_PORT || '5432'),
  user: process.env.DB_USER || 'postgres',
  password: process.env.DB_PASSWORD || 'postgres-secure-password',
  database: process.env.DB_NAME || 'ecommerce',
  max: 10,
  idleTimeoutMillis: 30000,
  connectionTimeoutMillis: 2000,
});

// Redis Client
const redisClient = createClient({ url: REDIS_URL });
redisClient.on('error', (err) => logger.error(`Redis Error: ${err.message}`));

let dbReady = false;
let redisReady = false;

// Retry connecting to Postgres
const initDbConnection = async (retries = 10, delay = 3000) => {
  while (retries > 0) {
    try {
      const client = await pool.connect();
      logger.info('Connected to PostgreSQL successfully.');
      client.release();
      dbReady = true;
      break;
    } catch (err) {
      retries -= 1;
      logger.warn(`Postgres connection failed (${err.message}). Retries remaining: ${retries}`);
      await new Promise((res) => setTimeout(res, delay));
    }
  }
};

// Retry connecting to Redis
const initRedisConnection = async (retries = 10, delay = 3000) => {
  while (retries > 0) {
    try {
      await redisClient.connect();
      logger.info('Connected to Redis successfully.');
      redisReady = true;
      break;
    } catch (err) {
      retries -= 1;
      logger.warn(`Redis connection failed (${err.message}). Retries remaining: ${retries}`);
      await new Promise((res) => setTimeout(res, delay));
    }
  }
};

const initServices = async () => {
  await Promise.all([initDbConnection(), initRedisConnection()]);
};

initServices();

// JWT Authentication Middleware
const authenticateToken = (req, res, next) => {
  const authHeader = req.headers['authorization'];
  const token = authHeader && authHeader.split(' ')[1];

  if (!token) return res.status(401).json({ message: 'Missing Authorization Token' });

  jwt.verify(token, JWT_SECRET, (err, user) => {
    if (err) return res.status(403).json({ message: 'Invalid or Expired Token' });
    req.user = user;
    next();
  });
};

// ==========================================
// 1. Health Endpoints
// ==========================================
app.get(['/api/health', '/health', '/api/users/health', '/api/products/health', '/api/orders/health', '/api/notifications/health'], (req, res) => {
  res.json({
    status: 'UP',
    service: 'backend-api',
    database: dbReady ? 'CONNECTED' : 'DISCONNECTED',
    redis: redisReady ? 'CONNECTED' : 'DISCONNECTED',
    uptime: process.uptime()
  });
});

// ==========================================
// 2. User Authentication Endpoints
// ==========================================
app.post('/api/users/register', async (req, res) => {
  const { username, email, password } = req.body;
  if (!username || !email || !password) {
    return res.status(400).json({ message: 'Missing required signup fields' });
  }

  try {
    const salt = await bcrypt.genSalt(10);
    const passwordHash = await bcrypt.hash(password, salt);

    const result = await pool.query(
      'INSERT INTO users (username, email, password_hash) VALUES ($1, $2, $3) RETURNING id, username, email',
      [username, email, passwordHash]
    );
    logger.info(`User registered: ${username}`);
    res.status(201).json(result.rows[0]);
  } catch (err) {
    if (err.code === '23505') {
      return res.status(409).json({ message: 'Username or email already exists' });
    }
    logger.error(`Registration error: ${err.message}`);
    res.status(500).json({ message: 'Internal Server Error' });
  }
});

app.post('/api/users/login', async (req, res) => {
  const { username, password } = req.body;
  if (!username || !password) {
    return res.status(400).json({ message: 'Missing username or password' });
  }

  try {
    const result = await pool.query('SELECT * FROM users WHERE username = $1', [username]);
    if (result.rows.length === 0) {
      return res.status(401).json({ message: 'Invalid credentials' });
    }

    const user = result.rows[0];
    const isMatch = await bcrypt.compare(password, user.password_hash);
    if (!isMatch) {
      return res.status(401).json({ message: 'Invalid credentials' });
    }

    const token = jwt.sign({ id: user.id, username: user.username }, JWT_SECRET, { expiresIn: '24h' });
    logger.info(`User logged in: ${username}`);
    res.json({ token, user: { id: user.id, username: user.username, email: user.email } });
  } catch (err) {
    logger.error(`Login error: ${err.message}`);
    res.status(500).json({ message: 'Internal Server Error' });
  }
});

// ==========================================
// 3. Product Catalog Endpoints
// ==========================================
app.get('/api/products', async (req, res) => {
  try {
    if (redisReady) {
      const cached = await redisClient.get('products_list');
      if (cached) {
        return res.json(JSON.parse(cached));
      }
    }

    const result = await pool.query('SELECT id, name, price, category, description FROM products ORDER BY id ASC');
    const products = result.rows;

    if (redisReady && products.length > 0) {
      await redisClient.set('products_list', JSON.stringify(products), { EX: 60 });
    }

    res.json(products);
  } catch (err) {
    logger.error(`Get products error: ${err.message}`);
    res.status(500).json({ message: 'Failed to retrieve products' });
  }
});

app.get('/api/products/search', async (req, res) => {
  const { q } = req.query;
  if (!q) {
    return res.status(400).json({ message: 'Query parameter "q" is required' });
  }

  try {
    const result = await pool.query(
      'SELECT id, name, price, category, description FROM products WHERE name ILIKE $1 OR description ILIKE $1 OR category ILIKE $1',
      [`%${q}%`]
    );
    res.json(result.rows);
  } catch (err) {
    logger.error(`Search error: ${err.message}`);
    res.status(500).json({ message: 'Search query failed' });
  }
});

app.get('/api/products/:id', async (req, res) => {
  const { id } = req.params;
  try {
    const result = await pool.query('SELECT id, name, price, category, description FROM products WHERE id = $1', [id]);
    if (result.rows.length === 0) {
      return res.status(404).json({ message: 'Product not found' });
    }
    res.json(result.rows[0]);
  } catch (err) {
    logger.error(`Get product ${id} error: ${err.message}`);
    res.status(500).json({ message: 'Failed to fetch product' });
  }
});

// ==========================================
// 4. Order Management Endpoints
// ==========================================
app.post('/api/orders', authenticateToken, async (req, res) => {
  const userId = req.user.id;
  const { items, total_amount } = req.body;

  if (!items || !items.length || !total_amount) {
    return res.status(400).json({ message: 'Invalid order payload. Must contain items and total_amount.' });
  }

  const client = await pool.connect();
  try {
    await client.query('BEGIN');

    const orderRes = await client.query(
      'INSERT INTO orders (user_id, total_amount, status) VALUES ($1, $2, $3) RETURNING id, user_id, total_amount, status, created_at',
      [userId, total_amount, 'Created']
    );
    const order = orderRes.rows[0];

    for (const item of items) {
      await client.query(
        'INSERT INTO order_items (order_id, product_id, quantity, price) VALUES ($1, $2, $3, $4)',
        [order.id, item.product_id, item.quantity, item.price]
      );
    }

    await client.query('COMMIT');
    logger.info(`Order #${order.id} placed successfully by user ${userId}`);

    // Publish order event to Redis channel for Analytics Service
    if (redisReady) {
      const orderEvent = JSON.stringify({
        orderId: order.id,
        userId: order.user_id,
        total: order.total_amount,
        itemsCount: items.length,
        timestamp: new Date().toISOString()
      });
      redisClient.publish('orders_stream', orderEvent).catch((err) => {
        logger.warn(`Failed to publish order event to Redis: ${err.message}`);
      });
    }

    res.status(201).json({ message: 'Order placed successfully', ...order, items });
  } catch (err) {
    await client.query('ROLLBACK');
    logger.error(`Order creation transaction failed: ${err.message}`);
    res.status(500).json({ message: 'Order processing error' });
  } finally {
    client.release();
  }
});

app.get('/api/orders', authenticateToken, async (req, res) => {
  const userId = req.user.id;
  try {
    const result = await pool.query(
      'SELECT id, user_id, total_amount, status, created_at FROM orders WHERE user_id = $1 ORDER BY created_at DESC',
      [userId]
    );
    res.json(result.rows);
  } catch (err) {
    logger.error(`Fetch orders error: ${err.message}`);
    res.status(500).json({ message: 'Failed to retrieve orders' });
  }
});

// ==========================================
// 5. Notifications & Async Job Dispatch Endpoints
// ==========================================
app.post('/api/notifications', async (req, res) => {
  const { type, email, payload } = req.body;
  if (!type || !email || !payload) {
    return res.status(400).json({ message: 'Missing notification arguments: type, email, payload.' });
  }

  try {
    if (!redisReady) {
      return res.status(503).json({ message: 'Task queue unavailable.' });
    }

    const task = JSON.stringify({
      type,
      email,
      payload,
      timestamp: new Date().toISOString()
    });

    // Enqueue task to Redis List for Worker service
    await redisClient.lPush('notifications_queue', task);
    logger.info(`Enqueued task "${type}" for ${email}`);
    res.json({ status: 'Queued', queue: 'notifications_queue' });
  } catch (err) {
    logger.error(`Failed to enqueue task: ${err.message}`);
    res.status(500).json({ message: 'Internal Server Error' });
  }
});

app.listen(PORT, () => {
  logger.info(`Backend API server running on port ${PORT}`);
});
