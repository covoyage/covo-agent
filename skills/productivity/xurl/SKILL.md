---
name: xurl
description: "X/Twitter CLI: post, search, read, DM, media upload, and v2 API access."
version: 1.0.0
author: Covo Agent
license: MIT
platforms: [linux, macos, windows]
prerequisites:
  commands: [python3]
metadata:
  tags: [twitter, x, social, posting, search]
---

# X/Twitter

```bash
pip install tweepy
export TWITTER_BEARER_TOKEN="..."
export TWITTER_API_KEY="..." TWITTER_API_SECRET="..."

# Search tweets
python3 -c "
import tweepy
client = tweepy.Client(bearer_token='$TWITTER_BEARER_TOKEN')
tweets = client.search_recent_tweets(query='python programming', max_results=10)
for t in tweets.data or []: print(t.text[:100])
"

# Post tweet
python3 -c "
client = tweepy.Client(consumer_key='$K', consumer_secret='$S', access_token='$T', access_token_secret='$TS')
client.create_tweet(text='Hello from Covo Agent!')
"

# Read user timeline
python3 -c "
user = client.get_user(username='username')
tweets = client.get_users_tweets(id=user.data.id, max_results=10)
for t in tweets.data: print(f'{t.created_at}: {t.text[:100]}')
"

# Upload media
python3 -c "
auth = tweepy.OAuth1UserHandler(consumer_key='...')
api = tweepy.API(auth)
media = api.media_upload('image.png')
client.create_tweet(text='Check this out', media_ids=[media.media_id])
"
```
