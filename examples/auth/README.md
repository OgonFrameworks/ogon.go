# Auth example

Session + passkey login, role-gated route, CSRF, rate-limit, lockout.

## Run

```bash
export OGON_SESSION_SECRET=$(openssl rand -hex 32)
ogon migrate run
ogon dev

# signup
curl :3000/signup -d '{"email":"a@b.c","password":"..."}'

# login
curl :3000/login -d '{"email":"a@b.c","password":"..."}'

# admin (403 without role)
curl :3000/admin
```

## Files

```
auth/
├── ogon.yaml
├── models/User.go
├── models/Credential.go
├── routes/auth.go         # /login, /logout, /passkey/*
├── routes/admin.go        # role-gated route
├── handlers/auth.go
└── auth_test.go           # matrix test
```

## Test

```bash
ogon test -run TestAuth
```

<!-- MIT License — Copyright (c) 2026 OgonFrameworks. All rights reserved. -->
