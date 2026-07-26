const axios = require('axios');

const TARGET_URL = process.env.TARGET_URL || 'http://localhost';
const CONCURRENCY = parseInt(process.env.CONCURRENCY || '5');
const INTERVAL_MS = parseInt(process.env.INTERVAL_MS || '1000');

let successCount = 0;
let errorCount = 0;

const delay = (ms) => new Promise((resolve) => setTimeout(resolve, ms));

const getRandomItem = (arr) => arr[Math.floor(Math.random() * arr.length)];

// Generate a random user
const generateUserData = () => {
  const rand = Math.floor(Math.random() * 1000000);
  return {
    username: `user_${rand}`,
    email: `user_${rand}@example.com`,
    password: 'SecurePassword123!'
  };
};

const simulateUserSession = async () => {
  const userData = generateUserData();
  const client = axios.create({
    baseURL: TARGET_URL,
    timeout: 5000
  });

  try {
    // 1. Sign Up
    await client.post('/api/users/register', userData);
    successCount++;

    // 2. Log In
    const loginRes = await client.post('/api/users/login', {
      username: userData.username,
      password: userData.password
    });
    const token = loginRes.data.token;
    successCount++;

    // Set authorization header for subsequent calls
    const authHeaders = { Authorization: `Bearer ${token}` };

    // 3. Browse Products & Search (Hits cache and DB)
    await client.get('/api/products');
    successCount++;

    const queries = ['Shirt', 'Mug', 'Keycap', 'Plushie'];
    await client.get(`/api/products/search?q=${getRandomItem(queries)}`);
    successCount++;

    // 4. Place an Order (Postgres Insert + Kafka Broadcast)
    const orderPayload = {
      items: [
        { product_id: 'p1', quantity: Math.floor(Math.random() * 3) + 1, price: 29.99 },
        { product_id: 'p3', quantity: 1, price: 24.99 }
      ],
      total_amount: 54.98
    };
    const orderRes = await client.post('/api/orders', orderPayload, { headers: authHeaders });
    successCount++;

    // 5. Query Order History
    await client.get('/api/orders', { headers: authHeaders });
    successCount++;

    // 6. Trigger Notification (RabbitMQ publish)
    await client.post('/api/notifications', {
      type: 'email',
      email: userData.email,
      payload: {
        orderId: orderRes.data.id,
        text: 'Thank you for your order! Your invoice is being processed.'
      }
    });
    successCount++;

  } catch (err) {
    errorCount++;
    console.error(`[Session Error] ${err.message} - Endpoint: ${err.config?.url || 'unknown'}`);
  }
};

const run = async () => {
  console.log(`Starting load generator targeting: ${TARGET_URL}`);
  console.log(`Concurrency: ${CONCURRENCY} sessions, Interval: ${INTERVAL_MS}ms`);

  // Report statistics every 10 seconds
  setInterval(() => {
    console.log(`[STATUS] Successful requests: ${successCount} | Failed requests: ${errorCount}`);
  }, 10000);

  while (true) {
    const sessions = Array.from({ length: CONCURRENCY }, () => simulateUserSession());
    await Promise.all(sessions);
    await delay(INTERVAL_MS);
  }
};

run().catch(console.error);
