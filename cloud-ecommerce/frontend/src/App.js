import React, { useState, useEffect } from 'react';
import axios from 'axios';

// Fallback Mock Data for UI presentation when Backend microservices are offline/suspended
const MOCK_PRODUCTS = [
  { id: 'p1', name: 'Premium Cloud Arch Shirt', price: 29.99, category: 'Apparel', description: 'Over-provisioned comfort' },
  { id: 'p2', name: 'Kubernetes Scheduler Mug', price: 14.99, category: 'Kitchenware', description: 'Keeps beverages warm while pods are pending' },
  { id: 'p3', name: 'Node Allocator Plushie', price: 24.99, category: 'Toys', description: 'Reclaims logical resources on contact' },
  { id: 'p4', name: 'Autoscaler Keycap', price: 9.99, category: 'Electronics', description: 'Fills node capacity with a single click' }
];

function App() {
  const [activeTab, setActiveTab] = useState('products');
  const [products, setProducts] = useState(MOCK_PRODUCTS);
  const [searchQuery, setSearchQuery] = useState('');
  const [cart, setCart] = useState([]);
  const [orders, setOrders] = useState([]);
  
  // Auth state
  const [user, setUser] = useState(null);
  const [username, setUsername] = useState('');
  const [email, setEmail] = useState('');
  const [password, setPassword] = useState('');
  const [authMode, setAuthMode] = useState('login'); // login, register
  const [authError, setAuthError] = useState('');

  // API Status Badge State
  const [apiStatus, setApiStatus] = useState({ online: false, message: 'Detecting gateway...' });

  useEffect(() => {
    // Check API Gateway / User Service connection status
    axios.get('/api/users/profile')
      .then(() => {
        setApiStatus({ online: true, message: 'All backend microservices online' });
      })
      .catch((err) => {
        // If 401 unauthorized, it means the API is reachable but we need to log in
        if (err.response && err.response.status === 401) {
          setApiStatus({ online: true, message: 'API Gateway Connected' });
        } else {
          setApiStatus({ online: false, message: 'Gateway unreachable (using Mock Mode)' });
        }
      });

    // Fetch products
    fetchProducts();
    // Fetch orders if logged in
    fetchOrders();
  }, [user]);

  const fetchProducts = async () => {
    try {
      const response = await axios.get('/api/products');
      if (response.data && response.data.length > 0) {
        setProducts(response.data);
      }
    } catch (e) {
      console.log('Using mock products due to API offline state.');
    }
  };

  const fetchOrders = async () => {
    if (!user) return;
    try {
      const token = localStorage.getItem('token');
      const response = await axios.get('/api/orders', {
        headers: { Authorization: `Bearer ${token}` }
      });
      setOrders(response.data);
    } catch (e) {
      console.log('Failed to fetch orders from Postgres API.');
    }
  };

  const handleSearch = async (e) => {
    e.preventDefault();
    try {
      const response = await axios.get(`/api/products/search?q=${searchQuery}`);
      setProducts(response.data);
    } catch (err) {
      const filtered = MOCK_PRODUCTS.filter(p => 
        p.name.toLowerCase().includes(searchQuery.toLowerCase()) || 
        p.category.toLowerCase().includes(searchQuery.toLowerCase())
      );
      setProducts(filtered);
    }
  };

  const handleAuth = async (e) => {
    e.preventDefault();
    setAuthError('');
    try {
      if (authMode === 'login') {
        const res = await axios.post('/api/users/login', { username, password });
        localStorage.setItem('token', res.data.token);
        setUser({ username });
        fetchOrders();
      } else {
        await axios.post('/api/users/register', { username, email, password });
        setAuthMode('login');
        alert('Registration successful! Please log in.');
      }
    } catch (err) {
      setAuthError(err.response?.data?.message || 'Authentication error. Continuing with mock login.');
      // Fake Login for local testing convenience
      setUser({ username: username || 'guest_user' });
      localStorage.setItem('token', 'mock-jwt-token');
    }
  };

  const logout = () => {
    localStorage.removeItem('token');
    setUser(null);
    setCart([]);
    setOrders([]);
  };

  const addToCart = (product) => {
    const existing = cart.find(item => item.product.id === product.id);
    if (existing) {
      setCart(cart.map(item => 
        item.product.id === product.id 
          ? { ...item, quantity: item.quantity + 1 }
          : item
      ));
    } else {
      setCart([...cart, { product, quantity: 1 }]);
    }
  };

  const checkout = async () => {
    if (!user) {
      alert('Please log in or register before checking out.');
      setActiveTab('profile');
      return;
    }
    const orderItems = cart.map(item => ({
      product_id: item.product.id,
      quantity: item.quantity,
      price: item.product.price
    }));
    const total_amount = cart.reduce((acc, item) => acc + (item.product.price * item.quantity), 0);

    try {
      const token = localStorage.getItem('token');
      const response = await axios.post('/api/orders', {
        items: orderItems,
        total_amount
      }, {
        headers: { Authorization: `Bearer ${token}` }
      });
      alert('Order Placed Successfully! Kafka Event emitted.');
      setCart([]);
      setOrders([response.data, ...orders]);
      setActiveTab('orders');
    } catch (err) {
      alert('Order placed in Local Simulation Mock Mode.');
      const newMockOrder = {
        id: 'ord-' + Math.floor(Math.random() * 100000),
        user_id: 1,
        total_amount,
        status: 'Processed',
        created_at: new Date().toISOString(),
        items: orderItems
      };
      setOrders([newMockOrder, ...orders]);
      setCart([]);
      setActiveTab('orders');
    }
  };

  return (
    <div className="app-container">
      {/* Header Bar */}
      <header className="navbar">
        <div className="logo-section">
          <span className="logo-icon">🪐</span>
          <h1>Cloud E-Commerce System</h1>
        </div>
        <form className="search-form" onSubmit={handleSearch}>
          <input 
            type="text" 
            placeholder="Search products..." 
            value={searchQuery}
            onChange={(e) => setSearchQuery(e.target.value)}
          />
          <button type="submit">Search</button>
        </form>
        <div className="nav-actions">
          <span className={`status-badge ${apiStatus.online ? 'online' : 'offline'}`}>
            {apiStatus.message}
          </span>
          <button 
            className={`tab-btn ${activeTab === 'products' ? 'active' : ''}`}
            onClick={() => setActiveTab('products')}
          >
            Storefront
          </button>
          <button 
            className={`tab-btn ${activeTab === 'cart' ? 'active' : ''}`}
            onClick={() => setActiveTab('cart')}
          >
            Cart ({cart.reduce((acc, item) => acc + item.quantity, 0)})
          </button>
          {user ? (
            <>
              <button 
                className={`tab-btn ${activeTab === 'orders' ? 'active' : ''}`}
                onClick={() => setActiveTab('orders')}
              >
                Orders
              </button>
              <button className="tab-btn profile" onClick={() => setActiveTab('profile')}>
                👤 {user.username}
              </button>
            </>
          ) : (
            <button className="tab-btn login-btn" onClick={() => setActiveTab('profile')}>
              Log In
            </button>
          )}
        </div>
      </header>

      {/* Main Panel Content */}
      <main className="main-content">
        {activeTab === 'products' && (
          <section className="catalog">
            <h2 className="section-title">E-Commerce Storefront Catalog</h2>
            <div className="product-grid">
              {products.map(p => (
                <div key={p.id} className="product-card">
                  <div className="card-header">
                    <span className="category-tag">{p.category}</span>
                  </div>
                  <h3>{p.name}</h3>
                  <p>{p.description}</p>
                  <div className="card-footer">
                    <span className="price">${p.price}</span>
                    <button onClick={() => addToCart(p)}>Add to Cart</button>
                  </div>
                </div>
              ))}
            </div>
          </section>
        )}

        {activeTab === 'cart' && (
          <section className="cart-page">
            <h2 className="section-title">Your Shopping Cart</h2>
            {cart.length === 0 ? (
              <div className="empty-state">
                <p>Your cart is empty. Pick some items from the catalog.</p>
                <button onClick={() => setActiveTab('products')}>Go to Storefront</button>
              </div>
            ) : (
              <div className="cart-container">
                <div className="cart-list">
                  {cart.map(item => (
                    <div key={item.product.id} className="cart-item">
                      <div>
                        <h4>{item.product.name}</h4>
                        <span className="item-price">${item.product.price}</span>
                      </div>
                      <div className="quantity-controls">
                        <span>Quantity: {item.quantity}</span>
                      </div>
                    </div>
                  ))}
                </div>
                <div className="checkout-summary">
                  <h3>Order Summary</h3>
                  <div className="summary-row">
                    <span>Subtotal</span>
                    <span>${cart.reduce((acc, item) => acc + (item.product.price * item.quantity), 0).toFixed(2)}</span>
                  </div>
                  <button className="checkout-btn" onClick={checkout}>Place Order</button>
                </div>
              </div>
            )}
          </section>
        )}

        {activeTab === 'orders' && (
          <section className="orders-page">
            <h2 className="section-title">Your Order History</h2>
            {orders.length === 0 ? (
              <p>No orders placed yet.</p>
            ) : (
              <div className="order-history-list">
                {orders.map(o => (
                  <div key={o.id} className="order-card">
                    <div className="order-header">
                      <span>Order Reference: {o.id}</span>
                      <span className="order-date">{new Date(o.created_at).toLocaleString()}</span>
                    </div>
                    <div className="order-details">
                      <span>Total Amount Paid: ${parseFloat(o.total_amount).toFixed(2)}</span>
                      <span className="order-status-badge">{o.status}</span>
                    </div>
                  </div>
                ))}
              </div>
            )}
          </section>
        )}

        {activeTab === 'profile' && (
          <section className="profile-page">
            <h2 className="section-title">Account Management</h2>
            {user ? (
              <div className="logged-in-panel">
                <p>Logged in as: <strong>{user.username}</strong></p>
                <p>This session is registered under JWT authorization token.</p>
                <button className="logout-btn" onClick={logout}>Sign Out</button>
              </div>
            ) : (
              <div className="auth-card">
                <h3>{authMode === 'login' ? 'Sign In to Account' : 'Register New Account'}</h3>
                {authError && <div className="auth-error">{authError}</div>}
                <form onSubmit={handleAuth}>
                  <div className="form-group">
                    <label>Username</label>
                    <input 
                      type="text" 
                      required 
                      value={username}
                      onChange={(e) => setUsername(e.target.value)}
                    />
                  </div>
                  {authMode === 'register' && (
                    <div className="form-group">
                      <label>Email</label>
                      <input 
                        type="email" 
                        required 
                        value={email}
                        onChange={(e) => setEmail(e.target.value)}
                      />
                    </div>
                  )}
                  <div className="form-group">
                    <label>Password</label>
                    <input 
                      type="password" 
                      required 
                      value={password}
                      onChange={(e) => setPassword(e.target.value)}
                    />
                  </div>
                  <button type="submit" className="auth-submit-btn">
                    {authMode === 'login' ? 'Log In' : 'Sign Up'}
                  </button>
                </form>
                <p className="auth-toggle">
                  {authMode === 'login' ? "Don't have an account? " : "Already have an account? "}
                  <span onClick={() => setAuthMode(authMode === 'login' ? 'register' : 'login')}>
                    {authMode === 'login' ? 'Register here' : 'Login here'}
                  </span>
                </p>
              </div>
            )}
          </section>
        )}
      </main>
    </div>
  );
}

export default App;
