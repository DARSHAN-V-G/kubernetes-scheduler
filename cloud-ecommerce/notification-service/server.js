const express = require('express');
const amqp = require('amqplib');
const { logger, morganMiddleware } = require('./logger');

const app = express();
const PORT = process.env.PORT || 3000;

app.use(express.json());
app.use(morganMiddleware);

// RabbitMQ Config
const rabbitmqUrl = process.env.RABBITMQ_URL || 'amqp://guest:guest@rabbitmq:5672';
let connection;
let channel;
let amqpConnected = false;

// Retry connecting to RabbitMQ
const connectRabbitMQ = async (retries = 10, delay = 5000) => {
  while (retries > 0) {
    try {
      connection = await amqp.connect(rabbitmqUrl);
      channel = await connection.createChannel();
      await channel.assertQueue('notifications', { durable: true });
      logger.info('Connected to RabbitMQ successfully. Queue: "notifications" initialized.');
      amqpConnected = true;
      break;
    } catch (err) {
      retries -= 1;
      logger.warn(`RabbitMQ connection failed. Retries remaining: ${retries}. Retrying in ${delay / 1000}s...`);
      await new Promise(res => setTimeout(res, delay));
    }
  }
  if (!amqpConnected) {
    logger.error('Notification Service failed to connect to RabbitMQ. Exiting.');
    process.exit(1);
  }
};

connectRabbitMQ();

// Service Health Check
app.get('/api/notifications/health', (req, res) => {
  res.json({ status: 'UP', service: 'notification-service', rabbitmq: amqpConnected });
});

// POST notification (publishes message to RabbitMQ queue)
app.post('/api/notifications', async (req, res) => {
  const { type, email, payload } = req.body;
  if (!type || !email || !payload) {
    return res.status(400).json({ message: 'Missing notification arguments: type, email, payload.' });
  }

  try {
    if (!amqpConnected) {
      return res.status(503).json({ message: 'Message Broker is unavailable.' });
    }

    const message = JSON.stringify({ type, email, payload, timestamp: new Date().toISOString() });
    
    // Publish message persistent to ensure durability across worker failures
    channel.sendToQueue('notifications', Buffer.from(message), {
      persistent: true
    });
    
    logger.info(`Notification queued for: ${email} (Type: ${type})`);
    res.json({ status: 'Queued', queue: 'notifications' });
  } catch (err) {
    logger.error(`Failed to publish message: ${err.message}`);
    res.status(500).json({ message: 'Internal Server Error' });
  }
});

app.listen(PORT, () => {
  logger.info(`Notification Service listening on port ${PORT}`);
});
