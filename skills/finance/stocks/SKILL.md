---
name: stocks
description: "Stock market data: quotes, history, search, comparison, and crypto via Yahoo Finance."
version: 1.0.0
author: Covo Agent
license: MIT
platforms: [linux, macos, windows]
prerequisites:
  commands: [python3]
metadata:
  tags: [stocks, finance, trading, crypto, yahoo, market, investment]
  related_skills: [excel-author]
---

# Stocks — Market Data & Analysis

Fetch stock market data, historical prices, company information, and
cryptocurrency data using Yahoo Finance.

## Installation

```bash
pip install yfinance
```

## When to Use

- "what's the price of AAPL?"
- "compare TSLA vs F over 5 years"
- "show me the historical performance of NVDA"
- "what's Bitcoin at?"
- "search for a stock ticker"

## Quick Price Lookup

```python
import yfinance as yf

# Single ticker
ticker = yf.Ticker("AAPL")
info = ticker.info

print(f"Price: ${info.get('currentPrice', info.get('regularMarketPrice', 'N/A'))}")
print(f"Market Cap: ${info.get('marketCap', 0):,}")
print(f"P/E Ratio: {info.get('trailingPE', 'N/A')}")
print(f"52w Range: ${info.get('fiftyTwoWeekLow', 0)} - ${info.get('fiftyTwoWeekHigh', 0)}")
```

## Historical Data

```python
# Last month daily
hist = ticker.history(period="1mo")
print(hist[["Open", "High", "Low", "Close", "Volume"]].tail())

# Specific date range
hist = ticker.history(start="2024-01-01", end="2024-12-31")

# Key metrics
print(f"Mean close: ${hist['Close'].mean():.2f}")
print(f"Volatility: {hist['Close'].std():.2f}")
print(f"Max price: ${hist['High'].max():.2f}")
```

## Compare Multiple Stocks

```python
tickers = ["AAPL", "MSFT", "GOOGL", "AMZN", "META"]
data = yf.download(tickers, period="6mo")["Close"]

# Calculate returns
returns = data.pct_change()
print(returns.mean())  # Average daily return

# Correlation matrix
print(data.corr())
```

## Company Information

```python
info = yf.Ticker("AAPL").info

key_metrics = {
    "Name": info.get("shortName"),
    "Sector": info.get("sector"),
    "Industry": info.get("industry"),
    "Employees": info.get("fullTimeEmployees"),
    "Revenue": info.get("totalRevenue"),
    "Profit Margin": info.get("profitMargins"),
    "Dividend Yield": info.get("dividendYield"),
    "Beta": info.get("beta"),
    "PEG Ratio": info.get("pegRatio"),
}
for k, v in key_metrics.items():
    print(f"{k}: {v}")
```

## Financial Statements

```python
# Quarterly financials
income = ticker.quarterly_financials
balance = ticker.quarterly_balance_sheet
cashflow = ticker.quarterly_cashflow

# Latest quarter
print(income.iloc[:, 0])  # Most recent quarter
```

## Crypto

```python
# Bitcoin
btc = yf.Ticker("BTC-USD")
hist = btc.history(period="1mo")
print(f"BTC: ${hist['Close'].iloc[-1]:.2f}")

# Ethereum
eth = yf.Ticker("ETH-USD")
print(f"ETH: ${eth.info.get('currentPrice', 'N/A')}")

# Top crypto tickers: BTC-USD, ETH-USD, SOL-USD, DOGE-USD, XRP-USD
```

## Search Ticker

```python
# If you're not sure of the ticker
import yfinance as yf
t = yf.Ticker("MSFT")
print(t.info.get("shortName"))  # Verify
```

## Common Tickers

| Category | Tickers |
|----------|---------|
| Tech | AAPL, MSFT, GOOGL, AMZN, META, NVDA, TSLA |
| Finance | JPM, GS, BAC, V, MA |
| Energy | XOM, CVX, SHEL |
| Index/ETF | SPY (S&P500), QQQ (NASDAQ), DIA (Dow), IWM (Russell) |
| Crypto | BTC-USD, ETH-USD, SOL-USD, DOGE-USD |
| China | BABA, JD, PDD, NIO, BIDU |
| Currency | EURUSD=X, GBPUSD=X, USDJPY=X |
