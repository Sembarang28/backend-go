# Contoh Migrasi: Standalone Proto ke Centralized Proto Repository

Dokumen ini menjelaskan strategi, arsitektur, dan langkah-langkah teknis untuk memindahkan definisi Protobuf dari *standalone repository* ke *centralized repository* terpusat untuk arsitektur microservices enterprise.

---

## 1. Mengapa Centralized Proto?

| Masalah pada Standalone Proto | Solusi dengan Centralized Proto |
| :--- | :--- |
| Duplikasi file `.proto` di banyak repo service. | Satu sumber kebenaran (*Single Source of Truth*). |
| Rawan *breaking changes* dan *tag number mismatch*. | Otomatisasi validasi kompatibilitas via CI/CD (`buf breaking`). |
| Setiap engineer harus mengompilasi protoc secara lokal. | SDK client/server di-generate dan di-publish otomatis sebagai Go Module. |
| Inkonsistensi versi kontrak antar service. | Semantic Versioning (`v1.2.0`) yang terkontrol dan terlacak di git tag. |

---

## 2. Struktur Centralized Proto Repository

Buat repository terpisah di organisasi GitHub, contoh: `github.com/your-org/proto-contracts`.

```plaintext
proto-contracts/
├── .github/
│   └── workflows/
│       └── publish-go.yml        # CI/CD otomatis generate Go SDK & push ke proto-go
├── buf.yaml                      # Konfigurasi Buf lint & breaking rules
├── buf.gen.yaml                  # Konfigurasi code generator Go
├── user/
│   └── v1/
│       └── user.proto            # Kontrak User Service
└── payment/
    └── v1/
        └── payment.proto         # Kontrak Payment Service
```

---

## 3. Konfigurasi Standar Tooling (Buf)

### A. `buf.yaml`
```yaml
version: v1
name: buf.build/your-org/contracts
breaking:
  use:
    - FILE
    - WIRE_JSON
lint:
  use:
    - DEFAULT
```

### B. `buf.gen.yaml`
```yaml
version: v1
plugins:
  - plugin: go
    out: gen/go
    opt:
      - paths=source_relative
  - plugin: go-grpc
    out: gen/go
    opt:
      - paths=source_relative
      - require_unimplemented_servers=false
```

### C. Contoh `example_user/v1/example_user.proto`
```protobuf
syntax = "proto3";

package example_user.v1;

option go_package = "github.com/your-org/proto-go/example_user/v1;exampleuserv1";

service ExampleUserService {
  rpc GetUser (GetUserRequest) returns (GetUserResponse);
}

message GetUserRequest {
  int32 id = 1;
}

message GetUserResponse {
  int32 id = 1;
  string name = 2;
  string email = 3;
}
```

---

## 4. Pipeline CI/CD di Centralized Proto Repo

Buat file `.github/workflows/publish-go.yml` pada repo `proto-contracts`:

```yaml
name: Generate & Publish Go Proto SDK

on:
  push:
    tags:
      - 'v*.*.*'
    branches:
      - main

jobs:
  lint-and-validate:
    runs-on: ubuntu-latest
    steps:
      - uses: actions/checkout@v4
      - uses: bufbuild/buf-setup-action@v1
        with:
          version: 'latest'

      - name: Lint Proto
        run: buf lint

      - name: Check Breaking Changes
        uses: bufbuild/buf-breaking-action@v1
        with:
          against: 'https://github.com/your-org/proto-contracts.git#branch=main'

  generate-and-publish:
    needs: lint-and-validate
    if: startsWith(github.ref, 'refs/tags/v')
    runs-on: ubuntu-latest
    steps:
      - uses: actions/checkout@v4
      - uses: bufbuild/buf-setup-action@v1

      - name: Install Protoc Go Plugins
        run: |
          go install google.golang.org/protobuf/cmd/protoc-gen-go@latest
          go install google.golang.org/grpc/cmd/protoc-gen-go-grpc@latest

      - name: Generate Go Code
        run: buf generate

      - name: Push to Dedicated Go Module Repo
        uses: cpina/github-action-push-to-another-repository@main
        env:
          API_TOKEN_GITHUB: ${{ secrets.BOT_GITHUB_TOKEN }}
        with:
          source-directory: 'gen/go'
          destination-github-username: 'your-org'
          destination-repository-name: 'proto-go'
          target-branch: 'main'
```

---

## 5. Langkah Migrasi di Repository Microservice (`backend-go`)

### Langkah 1: Konfigurasi Akses Private Go Module
Jika repo SDK bersifat private:
```bash
go env -w GOPRIVATE=github.com/your-org/*
```

### Langkah 2: Unduh Go SDK dari Centralized Repo
```bash
go get github.com/your-org/proto-go/example_user/v1@v1.0.0
```

### Langkah 3: Hapus Folder Proto Lokal
```bash
rm -rf proto/
```

### Langkah 4: Sesuaikan Import di Handler gRPC Server
Update `internal/handler/grpc/example_user_grpc_handler.go`:

```go
package grpc

import (
	"context"
	"errors"

	"backend-go/internal/domain"

	// Menggunakan centralized generated proto
	exampleuserv1 "github.com/your-org/proto-go/example_user/v1"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

type ExampleUserGRPCHandler struct {
	exampleuserv1.UnimplementedExampleUserServiceServer
	usecase domain.ExampleUserUsecase
}

func NewExampleUserGRPCHandler(u domain.ExampleUserUsecase) *ExampleUserGRPCHandler {
	return &ExampleUserGRPCHandler{usecase: u}
}

func (h *ExampleUserGRPCHandler) GetUser(ctx context.Context, req *exampleuserv1.GetUserRequest) (*exampleuserv1.GetUserResponse, error) {
	if req.GetId() <= 0 {
		return nil, status.Error(codes.InvalidArgument, "id must be greater than 0")
	}

	user, err := h.usecase.GetUser(ctx, int(req.GetId()))
	if err != nil {
		if errors.Is(err, domain.ErrExampleUserNotFound) {
			return nil, status.Error(codes.NotFound, err.Error())
		}
		return nil, status.Error(codes.Internal, "internal server error")
	}

	return &exampleuserv1.GetUserResponse{
		Id:    int32(user.ID),
		Name:  user.Name,
		Email: user.Email,
	}, nil
}
```

### Langkah 5: Registrasi ke Server gRPC di `cmd/api/main.go`
```go
import (
	exampleuserv1 "github.com/your-org/proto-go/example_user/v1"
)

// Registrasi handler ke gRPC Server
userGRPCHandler := handlergrpc.NewExampleUserGRPCHandler(userUsecase)
exampleuserv1.RegisterExampleUserServiceServer(grpcSrv, userGRPCHandler)
```

---

## 6. Aturan dan Best Practices Kontrak Proto

1. **Paket Versioning Semantik**:
   - Selalu sertakan versi pada nama package proto: `package example_user.v1;`
   - Jika terdapat *breaking change* yang fundamental, buat folder baru `example_user/v2/`.
2. **Tag Reservation**:
   - Jangan pernah mengubah nomor tag field (`id = 1;`).
   - Jika field dihapus, tandai dengan `reserved`:
     ```protobuf
     message GetUserResponse {
       reserved 4, 8;
       reserved "phone_number";
     }
     ```
3. **CI/CD Guard**:
   - Pasang bot PR status check untuk memastikan `buf breaking` selalu lolos sebelum merge ke `main`.
