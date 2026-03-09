import pandas as pd
import numpy as np
import matplotlib.pyplot as plt
from sklearn.ensemble import RandomForestRegressor
from sklearn.metrics import mean_absolute_error, mean_squared_error, r2_score

# Load data
df = pd.read_csv('sales.csv')
df['date'] = pd.to_datetime(df['date'])

# Aggregate to daily sales
daily_sales = df.groupby('date')['amount'].sum().reset_index()
daily_sales = daily_sales.sort_values('date').set_index('date')

print(f"Data range: {daily_sales.index.min()} to {daily_sales.index.max()}")
print(f"Total days: {len(daily_sales)}")
print(f"\nDaily sales summary:")
print(daily_sales['amount'].describe())

# Create features for time series forecasting
def create_features(df, lags=[1, 2, 3, 7], windows=[3, 7]):
    df = df.copy()
    
    # Lag features
    for lag in lags:
        df[f'lag_{lag}'] = df['amount'].shift(lag)
    
    # Rolling window features
    for window in windows:
        df[f'rolling_mean_{window}'] = df['amount'].rolling(window).mean().shift(1)
        df[f'rolling_std_{window}'] = df['amount'].rolling(window).std().shift(1)
    
    # Day of week
    df['day_of_week'] = df.index.dayofweek
    df['day_of_month'] = df.index.day
    
    return df

# Create features
daily_sales_features = create_features(daily_sales)

# Drop rows with NaN (from lag/rolling features)
daily_sales_features = daily_sales_features.dropna()

print(f"\nFeatures shape after dropping NaNs: {daily_sales_features.shape}")

# Define features and target
feature_cols = [col for col in daily_sales_features.columns if col != 'amount']
X = daily_sales_features[feature_cols]
y = daily_sales_features['amount']

# Train/test split (80/20)
split_idx = int(len(X) * 0.8)
X_train, X_test = X.iloc[:split_idx], X.iloc[split_idx:]
y_train, y_test = y.iloc[:split_idx], y.iloc[split_idx:]

print(f"\nTrain size: {len(X_train)}, Test size: {len(X_test)}")

# Train model
model = RandomForestRegressor(n_estimators=100, random_state=42, n_jobs=-1)
model.fit(X_train, y_train)

# Predictions
y_train_pred = model.predict(X_train)
y_test_pred = model.predict(X_test)

# Evaluate
train_mae = mean_absolute_error(y_train, y_train_pred)
test_mae = mean_absolute_error(y_test, y_test_pred)
train_rmse = np.sqrt(mean_squared_error(y_train, y_train_pred))
test_rmse = np.sqrt(mean_squared_error(y_test, y_test_pred))
train_r2 = r2_score(y_train, y_train_pred)
test_r2 = r2_score(y_test, y_test_pred)

print("\n=== Model Performance ===")
print(f"Train MAE: ${train_mae:.2f}, RMSE: ${train_rmse:.2f}, R²: {train_r2:.3f}")
print(f"Test  MAE: ${test_mae:.2f}, RMSE: ${test_rmse:.2f}, R²: {test_r2:.3f}")

# Feature importance
print("\n=== Feature Importance ===")
importance = pd.DataFrame({
    'feature': feature_cols,
    'importance': model.feature_importances_
}).sort_values('importance', ascending=False)
print(importance)

# Plot results
fig, axes = plt.subplots(2, 1, figsize=(12, 8))

# Full time series with predictions
axes[0].plot(daily_sales.index, daily_sales['amount'], label='Actual', alpha=0.7)
axes[0].plot(X_test.index, y_test_pred, label='Predicted (Test)', linewidth=2)
axes[0].axvline(X_test.index[0], color='red', linestyle='--', label='Train/Test Split')
axes[0].set_title('Daily Sales: Actual vs Predicted')
axes[0].set_ylabel('Sales ($)')
axes[0].legend()
axes[0].grid(True, alpha=0.3)

# Actual vs Predicted scatter
axes[1].scatter(y_test, y_test_pred, alpha=0.6)
axes[1].plot([y_test.min(), y_test.max()], [y_test.min(), y_test.max()], 'r--', lw=2)
axes[1].set_xlabel('Actual Sales ($)')
axes[1].set_ylabel('Predicted Sales ($)')
axes[1].set_title(f'Test Set: Actual vs Predicted (R² = {test_r2:.3f})')
axes[1].grid(True, alpha=0.3)

plt.tight_layout()
plt.savefig('forecast_results.png', dpi=150)
print("\nPlot saved to forecast_results.png")

# Save model for future use
import joblib
joblib.dump(model, 'sales_forecast_model.pkl')
print("Model saved to sales_forecast_model.pkl")
