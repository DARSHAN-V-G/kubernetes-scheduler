const express = require('express');
const { Pool } = require('pg');
const bcrypt = require('bcryptjs');
const jwt = require('jsonwebtoken');
const { logger, morganMiddleware } = require('./logger');

const app = express();
const PORT = process.env.PORT || 3000;
const JWT_SECRET = process.env.JWT_SECRET || 'super-secret-key';

app.use(express.json());
app.use(morganMiddleware);

// Configure Postgres Connection Pool
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

// Test and await PG Connection
const initDbConnection = async (retries = 5, delay = 5000) => {
  while (retries > 0) {
    try {
      const client = await pool.connect();
      logger.info('Connected to PostgreSQL database successfully.');
      client.release();
      break;
    } catch (err) {
      retries -= 1;
      logger.warn(`Postgres connection failed. Retries remaining: ${retries}. Retrying in ${delay / 1000}s...`);
      await new Promise(res => setTimeout(res, delay));
    }
  }
  if (retries === 0) {
    logger.error('Could not connect to PostgreSQL. Exiting.');
    process.exit(1);
  }
};

initDbConnection();

// Service Health Check
app.get('/api/users/health', (req, res) => {
  res.json({ status: 'UP', service: 'user-service' });
});

// Register User
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
    if (err.code === '23505') { // Duplicate key
      return res.status(409).json({ message: 'Username or email already exists' });
    }
    logger.error(`Registration error: ${err.message}`);
    res.status(500).json({ message: 'Internal Server Error' });
  }
});

// Login User
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

// Retrieve User Profile (Authenticated check via Gateway pass)
app.get('/api/users/profile', async (req, res) => {
  // If request went through API gateway, we can extract header variables or token
  const authHeader = req.headers['authorization'];
  const token = authHeader && authHeader.split(' ')[1];

  if (!token) return res.status(401).json({ message: 'Unauthorized profile request' });

  try {
    const decoded = jwt.verify(token, JWT_SECRET);
    const result = await pool.query('SELECT id, username, email, created_at FROM users WHERE id = $1', [decoded.id]);
    if (result.rows.length === 0) {
      return res.status(404).json({ message: 'User not found' });
    }
    res.json(result.rows[0]);
  } catch (err) {
    res.status(403).json({ message: 'Forbidden access' });
  }
});

app.listen(PORT, () => {
  logger.info(`User Service listening on port ${PORT}`);
});
