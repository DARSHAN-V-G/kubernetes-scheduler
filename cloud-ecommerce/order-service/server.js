const express = require('express');
const { Pool } = require('pg');
const { Kafka, Partitioners } = require('kafkajs');
const { logger, morganMiddleware } = require('./logger');

const app = express();
const PORT = process.env.PORT || 3000;

app.use(express.json());
app.use(morganMiddleware);

// Postgres Config
const pool = new Pool({
  host: process.env.DB_HOST || 'postgres',
  port: parseInt(process.env.DB_PORT || '5432'),
  user: process.env.DB_USER || 'postgres',
  password: process.env.DB_PASSWORD || 'postgres-secure-password',
  database: process.env.DB_NAME || 'ecommerce',
  max: 15,
  idleTimeoutMillis: 30000,
  connectionTimeoutMillis: 2000,
});

// Kafka Config
const kafkaBrokers = (process.env.KAFKA_BROKERS || 'kafka:9092').split(',');
const kafka = new Kafka({
  clientId: 'order-service',
  brokers: kafkaBrokers,
  retry: {
    initialRetryTime: 300,
    retries: 10
  }
});

const producer = kafka.producer({
  createPartitioner: Partitioners.LegacyPartitioner
});

let kafkaConnected = false;

// Test and await PG Connection
const initDbConnection = async (retries = 5, delay = 5000) => {
  while (retries > 0) {
    try {
      const client = await pool.connect();
      logger.info('Order Service connected to PostgreSQL successfully.');
      client.release();
      break;
    } catch (err) {
      retries -= 1;
      logger.warn(`Postgres connection failed. Retries remaining: ${retries}. Retrying in ${delay / 1000}s...`);
      await new Promise(res => setTimeout(res, delay));
    }
  }
  if (retries === 0) {
    logger.error('Order Service failed to connect to PostgreSQL. Exiting.');
    process.exit(1);
  }
};

// Connect to Kafka Broker
const initKafkaConnection = async (retries = 10, delay = 5000) => {
  while (retries > 0) {
    try {
      await producer.connect();
      logger.info('Connected to Kafka Broker successfully.');
      kafkaConnected = true;
      break;
    } catch (err) {
      retries -= 1;
      logger.warn(`Kafka connection failed. Retries remaining: ${retries}. Retrying in ${delay / 1000}s...`);
      await new Promise(res => setTimeout(res, delay));
    }
  }
  if (!kafkaConnected) {
    logger.error('Order Service failed to connect to Kafka. Exiting.');
    process.exit(1);
  }
};

const initConnections = async () => {
  await initDbConnection();
  await initKafkaConnection();
};

initConnections();

// Service Health Check
app.get('/api/orders/health', (req, res) => {
  res.json({ status: 'UP', service: 'order-service', kafka: kafkaConnected });
});

// Create Order (inserts into PG, publishes to Kafka)
app.post('/api/orders', async (req, res) => {
  // Read User ID passed from the API Gateway header
  const userId = req.headers['x-user-id'];
  const { items, total_amount } = req.body;

  if (!userId) {
    return res.status(401).json({ message: 'Unauthorized. User identification header missing.' });
  }
  if (!items || !items.length || !total_amount) {
    return res.status(400).json({ message: 'Invalid order structure' });
  }

  const client = await pool.connect();
  try {
    // Begin Database Transaction
    await client.query('BEGIN');

    const orderResult = await client.query(
      'INSERT INTO orders (user_id, total_amount, status) VALUES ($1, $2, $3) RETURNING *',
      [userId, total_amount, 'Created']
    );
    const order = orderResult.rows[0];

    // Insert Order Items
    for (const item of items) {
      await client.query(
        'INSERT INTO order_items (order_id, product_id, quantity, price) VALUES ($1, $2, $3, $4)',
        [order.id, item.product_id, item.quantity, item.price]
      );
    }

    await client.query('COMMIT');
    logger.info(`Order created in Postgres: ID ${order.id} for User ${userId}`);

    // Publish event to Kafka
    const orderPayload = {
      orderId: order.id,
      userId: userId,
      total: total_amount,
      items: items,
      createdAt: order.created_at
    };

    if (kafkaConnected) {
      await producer.send({
        topic: 'orders',
        messages: [
          { key: String(order.id), value: JSON.stringify(orderPayload) }
        ],
      });
      logger.info(`Emitted OrderCreated event to Kafka for Order ID: ${order.id}`);
    } else {
      logger.warn('Skipped emitting Kafka event - Broker is offline.');
    }

    res.status(201).json(order);
  } catch (err) {
    await client.query('ROLLBACK');
    logger.error(`Order execution failed: ${err.message}`);
    res.status(500).json({ message: 'Internal Server Error' });
  } finally {
    client.release();
  }
});

// Retrieve User Orders
app.get('/api/orders', async (req, res) => {
  const userId = req.headers['x-user-id'];
  if (!userId) {
    return res.status(401).json({ message: 'Unauthorized' });
  }

  try {
    const result = await pool.query(
      'SELECT * FROM orders WHERE user_id = $1 ORDER BY created_at DESC',
      [userId]
    );
    res.json(result.rows);
  } catch (err) {
    logger.error(`Error querying orders: ${err.message}`);
    res.status(500).json({ message: 'Internal Server Error' });
  }
});

app.listen(PORT, () => {
  logger.info(`Order Service listening on port ${PORT}`);
});
