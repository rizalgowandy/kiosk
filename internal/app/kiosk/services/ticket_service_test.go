// Copyright 2019 The Jibit Team. All rights reserved.
// Use of this source code is governed by an Apache Style license that can be found in the LICENSE.md file.

package services

import (
	"context"
	"io/ioutil"
	"path/filepath"
	"testing"
	"time"

	"github.com/docker/go-connections/nat"
	_ "github.com/golang-migrate/migrate/v4/database/postgres"
	_ "github.com/golang-migrate/migrate/v4/source/file"
	"github.com/jackc/pgx/v4/pgxpool"
	rpc "github.com/jibitters/kiosk/g/rpc/kiosk"
	"github.com/jibitters/kiosk/internal/app/kiosk/configuration"
	"github.com/jibitters/kiosk/internal/app/kiosk/database"
	"github.com/jibitters/kiosk/internal/pkg/logging"
	"github.com/jibitters/kiosk/test/containers"
	_ "github.com/lib/pq"
	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/wait"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

const firstMigrationSchema = `
-- Tickets table definition.
	CREATE TABLE tickets (
	    id                                 BIGSERIAL NOT NULL,
	    issuer                             VARCHAR(40) NOT NULL,
	    owner                              VARCHAR(40) NOT NULL,
	    subject                            VARCHAR(255) NOT NULL,
	    content                            TEXT NOT NULL,
	    metadata                           TEXT,
	    ticket_importance_level            VARCHAR(20) NOT NULL,
	    ticket_status                      VARCHAR(20) NOT NULL,
	    issued_at                          TIMESTAMP NOT NULL,
	    updated_at                         TIMESTAMP NOT NULL,
	    PRIMARY KEY (id)
	);

	CREATE INDEX idx_tickets_issuer_issued_at ON tickets (issuer, issued_at DESC);
	CREATE INDEX idx_tickets_owner_issued_at ON tickets (owner, issued_at DESC);
	CREATE INDEX idx_tickets_ticket_importance_level_ticket_status ON tickets (ticket_importance_level, ticket_status);

	-- Comments table definition.
	CREATE TABLE comments (
	    id                                 BIGSERIAL NOT NULL,
	    ticket_id                          BIGINT REFERENCES tickets,
	    owner                              VARCHAR(40) NOT NULL,
	    content                            TEXT NOT NULL,
	    metadata                           TEXT,
	    created_at                         TIMESTAMP NOT NULL,
	    updated_at                         TIMESTAMP NOT NULL,
	    PRIMARY KEY (id)
	);

	CREATE INDEX idx_comments_ticket_id ON comments (ticket_id);
	CREATE INDEX idx_comments_owner_created_at ON comments (owner, created_at DESC);`

func setupPostgresAndRunMigration() (testcontainers.Container, *pgxpool.Pool, error) {
	// Starting postgres container.
	containerPort, err := nat.NewPort("tcp", "5432")
	if err != nil {
		return nil, nil, err
	}

	request := testcontainers.ContainerRequest{
		Image:        "postgres:11",
		ExposedPorts: []string{"5432/tcp"},
		Env:          map[string]string{"POSTGRES_DB": "kiosk", "POSTGRES_USER": "kiosk", "POSTGRES_PASSWORD": "password"},
		WaitingFor:   wait.ForListeningPort(containerPort),
	}

	container, err := containers.NewContainer(request)
	if err != nil {
		return nil, nil, err
	}

	mappedPort, err := container.MappedPort(containers.ContainersContext, containerPort)
	if err != nil {
		return nil, nil, err
	}

	// Running database migration.
	directory, err := ioutil.TempDir("", "migration")
	if err != nil {
		return nil, nil, err
	}

	first, err := ioutil.TempFile(directory, "1_*.up.sql")
	if err != nil {
		return nil, nil, err
	}
	defer first.Close()

	first.WriteString(firstMigrationSchema)

	config := &configuration.Config{Postgres: configuration.PostgresConfig{
		Host:               "localhost",
		Port:               mappedPort.Int(),
		Name:               "kiosk",
		User:               "kiosk",
		Password:           "password",
		ConnectionTimeout:  10,
		MaxConnection:      8,
		SSLMode:            "disable",
		MigrationDirectory: "file://" + filepath.Dir(first.Name()),
	}}

	if err := database.Migrate(config); err != nil {
		return nil, nil, err
	}

	// Getting database connection pool.
	db, err := database.ConnectToDatabase(config)
	if err != nil {
		return nil, nil, err
	}

	return container, db, nil
}

func TestCreate_InvalidArgument(t *testing.T) {
	container, db, err := setupPostgresAndRunMigration()
	if err != nil {
		t.Logf("Error : %v", err)
		t.FailNow()
	}
	defer containers.CloseContainer(container)
	defer db.Close()

	service := NewTicketService(logging.New(logging.DebugLevel), db)

	ticket := &rpc.Ticket{
		Owner:                 "09203091992",
		Subject:               "Documentation",
		Content:               "Hello, i need some help about your technical documentation.",
		Metadata:              "{\"owner_ip\": \"185.186.187.188\"}",
		TicketImportanceLevel: rpc.TicketImportanceLevel_HIGH,
		TicketStatus:          rpc.TicketStatus_NEW,
	}
	createShouldReturnInvalidArgument(t, service, ticket, "create_ticket.empty_issuer")

	ticket = &rpc.Ticket{
		Issuer:                "Jibit",
		Owner:                 "",
		Subject:               "Documentation",
		Content:               "Hello, i need some help about your technical documentation.",
		Metadata:              "{\"owner_ip\": \"185.186.187.188\"}",
		TicketImportanceLevel: rpc.TicketImportanceLevel_HIGH,
		TicketStatus:          rpc.TicketStatus_NEW,
	}
	createShouldReturnInvalidArgument(t, service, ticket, "create_ticket.empty_owner")

	ticket = &rpc.Ticket{
		Issuer:                "Jibit",
		Owner:                 "09203091992",
		Subject:               " ",
		Content:               "Hello, i need some help about your technical documentation.",
		Metadata:              "{\"owner_ip\": \"185.186.187.188\"}",
		TicketImportanceLevel: rpc.TicketImportanceLevel_HIGH,
		TicketStatus:          rpc.TicketStatus_NEW,
	}
	createShouldReturnInvalidArgument(t, service, ticket, "create_ticket.empty_subject")

	ticket = &rpc.Ticket{
		Issuer:  "Jibit",
		Owner:   "09203091992",
		Subject: "Documentation",
		Content: "	",
		Metadata:              "{\"owner_ip\": \"185.186.187.188\"}",
		TicketImportanceLevel: rpc.TicketImportanceLevel_HIGH,
		TicketStatus:          rpc.TicketStatus_NEW,
	}
	createShouldReturnInvalidArgument(t, service, ticket, "create_ticket.empty_content")

	ticket = &rpc.Ticket{
		Issuer:                "Jibit",
		Owner:                 "09203091992",
		Subject:               "Documentation",
		Content:               "Hello, i need some help about your technical documentation.",
		Metadata:              "{\"owner_ip\": \"185.186.187.188\"}",
		TicketImportanceLevel: rpc.TicketImportanceLevel_HIGH,
		TicketStatus:          rpc.TicketStatus_RESOLVED,
	}
	createShouldReturnInvalidArgument(t, service, ticket, "create_ticket.invalid_status")
}

func TestCreate_DatabaseConnectionFailure(t *testing.T) {
	container, db, err := setupPostgresAndRunMigration()
	if err != nil {
		t.Logf("Error : %v", err)
		t.FailNow()
	}
	defer containers.CloseContainer(container)
	db.Close()

	service := NewTicketService(logging.New(logging.DebugLevel), db)

	ticket := &rpc.Ticket{
		Issuer:                "Jibit",
		Owner:                 "09203091992",
		Subject:               "Documentation",
		Content:               "Hello, i need some help about your technical documentation.",
		Metadata:              "{\"owner_ip\": \"185.186.187.188\"}",
		TicketImportanceLevel: rpc.TicketImportanceLevel_HIGH,
		TicketStatus:          rpc.TicketStatus_NEW,
	}
	createShouldReturnInternal(t, service, ticket, "create_ticket.failed")
}

func TestCreate_DatabaseNetworkFailure(t *testing.T) {
	container, db, err := setupPostgresAndRunMigration()
	if err != nil {
		t.Logf("Error : %v", err)
		t.FailNow()
	}
	containers.CloseContainer(container)
	defer db.Close()

	service := NewTicketService(logging.New(logging.DebugLevel), db)

	ticket := &rpc.Ticket{
		Issuer:                "Jibit",
		Owner:                 "09203091992",
		Subject:               "Documentation",
		Content:               "Hello, i need some help about your technical documentation.",
		Metadata:              "{\"owner_ip\": \"185.186.187.188\"}",
		TicketImportanceLevel: rpc.TicketImportanceLevel_HIGH,
		TicketStatus:          rpc.TicketStatus_NEW,
	}
	createShouldReturnInternal(t, service, ticket, "create_ticket.failed")
}

func TestCreate(t *testing.T) {
	container, db, err := setupPostgresAndRunMigration()
	if err != nil {
		t.Logf("Error : %v", err)
		t.FailNow()
	}
	defer containers.CloseContainer(container)
	defer db.Close()

	service := NewTicketService(logging.New(logging.DebugLevel), db)

	ticket := &rpc.Ticket{
		Issuer:                "Jibit",
		Owner:                 "09203091992",
		Subject:               "Documentation",
		Content:               "Hello, i need some help about your technical documentation.",
		Metadata:              "{\"owner_ip\": \"185.186.187.188\"}",
		TicketImportanceLevel: rpc.TicketImportanceLevel_HIGH,
		TicketStatus:          rpc.TicketStatus_NEW,
	}

	if _, err := service.Create(context.Background(), ticket); err != nil {
		t.Logf("Error : %v", err)
		t.FailNow()
	}
}

func TestRead_InvalidArgument(t *testing.T) {
	container, db, err := setupPostgresAndRunMigration()
	if err != nil {
		t.Logf("Error : %v", err)
		t.FailNow()
	}
	defer containers.CloseContainer(container)
	defer db.Close()

	service := NewTicketService(logging.New(logging.DebugLevel), db)

	id := &rpc.Id{Id: 0}
	readShouldReturnInvalidArgument(t, service, id, "read_ticket.invalid_id")
}

func TestRead_Notfound(t *testing.T) {
	container, db, err := setupPostgresAndRunMigration()
	if err != nil {
		t.Logf("Error : %v", err)
		t.FailNow()
	}
	defer containers.CloseContainer(container)
	defer db.Close()

	service := NewTicketService(logging.New(logging.DebugLevel), db)

	id := &rpc.Id{Id: 1}
	readShouldReturnNotfound(t, service, id, "read_ticket.not_found")
}

func TestRead_DatabaseConnectionFailure(t *testing.T) {
	container, db, err := setupPostgresAndRunMigration()
	if err != nil {
		t.Logf("Error : %v", err)
		t.FailNow()
	}
	defer containers.CloseContainer(container)
	db.Close()

	service := NewTicketService(logging.New(logging.DebugLevel), db)

	id := &rpc.Id{Id: 1}
	readShouldReturnInternal(t, service, id, "read_ticket.failed")
}

func TestRead_DatabaseNetworkFailure(t *testing.T) {
	container, db, err := setupPostgresAndRunMigration()
	if err != nil {
		t.Logf("Error : %v", err)
		t.FailNow()
	}
	containers.CloseContainer(container)
	defer db.Close()

	service := NewTicketService(logging.New(logging.DebugLevel), db)

	id := &rpc.Id{Id: 1}
	readShouldReturnInternal(t, service, id, "read_ticket.failed")
}

func TestRead(t *testing.T) {
	container, db, err := setupPostgresAndRunMigration()
	if err != nil {
		t.Logf("Error : %v", err)
		t.FailNow()
	}
	defer containers.CloseContainer(container)
	defer db.Close()

	service := NewTicketService(logging.New(logging.DebugLevel), db)

	ticket := &rpc.Ticket{
		Issuer:                "Jibit",
		Owner:                 "09203091992",
		Subject:               "Documentation",
		Content:               "Hello, i need some help about your technical documentation.",
		Metadata:              "{\"owner_ip\": \"185.186.187.188\"}",
		TicketImportanceLevel: rpc.TicketImportanceLevel_HIGH,
		TicketStatus:          rpc.TicketStatus_NEW,
	}

	if _, err := service.Create(context.Background(), ticket); err != nil {
		t.Logf("Error : %v", err)
		t.FailNow()
	}

	id := &rpc.Id{Id: 1}
	response, err := service.Read(context.Background(), id)
	if err != nil {
		t.Logf("Error : %v", err)
		t.FailNow()
	}

	if response.Id != id.Id {
		t.Logf("Actual: %v Expected: %v", response.Id, id.Id)
		t.FailNow()
	}

	if response.Issuer != ticket.Issuer {
		t.Logf("Actual: %v Expected: %v", response.Issuer, ticket.Issuer)
		t.FailNow()
	}

	if response.Owner != ticket.Owner {
		t.Logf("Actual: %v Expected: %v", response.Owner, ticket.Owner)
		t.FailNow()
	}

	if response.Subject != ticket.Subject {
		t.Logf("Actual: %v Expected: %v", response.Subject, ticket.Subject)
		t.FailNow()
	}

	if response.Content != ticket.Content {
		t.Logf("Actual: %v Expected: %v", response.Content, ticket.Content)
		t.FailNow()
	}

	if response.Metadata != ticket.Metadata {
		t.Logf("Actual: %v Expected: %v", response.Metadata, ticket.Metadata)
		t.FailNow()
	}

	if response.TicketImportanceLevel != ticket.TicketImportanceLevel {
		t.Logf("Actual: %v Expected: %v", response.TicketImportanceLevel, ticket.TicketImportanceLevel)
		t.FailNow()
	}

	if response.TicketStatus != rpc.TicketStatus_NEW {
		t.Logf("Actual: %v Expected: %v", response.TicketStatus, rpc.TicketStatus_NEW)
		t.FailNow()
	}

	if len(response.Comments) != 0 {
		t.Logf("Actual: %v Expected: %v", len(response.Comments), 0)
		t.FailNow()
	}

	parsedIssuedAtTime, _ := time.Parse(time.RFC3339Nano, response.IssuedAt)
	if !time.Now().UTC().After(parsedIssuedAtTime) {
		t.Logf("Issued at must be before now().")
		t.FailNow()
	}

	parsedUpdatedAtTime, _ := time.Parse(time.RFC3339Nano, response.UpdatedAt)
	if !time.Now().UTC().After(parsedUpdatedAtTime) {
		t.Logf("Updated at must be before now().")
		t.FailNow()
	}
}

func TestUpdate_InvalidArgument(t *testing.T) {
	container, db, err := setupPostgresAndRunMigration()
	if err != nil {
		t.Logf("Error : %v", err)
		t.FailNow()
	}
	defer containers.CloseContainer(container)
	defer db.Close()

	service := NewTicketService(logging.New(logging.DebugLevel), db)

	ticket := &rpc.Ticket{
		Id:           0,
		TicketStatus: rpc.TicketStatus_NEW,
	}
	updateShouldReturnInvalidArgument(t, service, ticket, "update_ticket.invalid_id")

	ticket = &rpc.Ticket{
		Id:           1,
		TicketStatus: rpc.TicketStatus_NEW,
	}
	updateShouldReturnInvalidArgument(t, service, ticket, "update_ticket.invalid_ticket_status")
}

func TestUpdate_Notfound(t *testing.T) {
	container, db, err := setupPostgresAndRunMigration()
	if err != nil {
		t.Logf("Error : %v", err)
		t.FailNow()
	}
	defer containers.CloseContainer(container)
	defer db.Close()

	service := NewTicketService(logging.New(logging.DebugLevel), db)

	ticket := &rpc.Ticket{
		Id:           1,
		TicketStatus: rpc.TicketStatus_RESOLVED,
	}
	updateShouldReturnNotfound(t, service, ticket, "update_ticket.not_found")
}

func TestUpdate_DatabaseConnectionFailure(t *testing.T) {
	container, db, err := setupPostgresAndRunMigration()
	if err != nil {
		t.Logf("Error : %v", err)
		t.FailNow()
	}
	defer containers.CloseContainer(container)
	db.Close()

	service := NewTicketService(logging.New(logging.DebugLevel), db)

	ticket := &rpc.Ticket{
		Id:           1,
		TicketStatus: rpc.TicketStatus_RESOLVED,
	}
	updateShouldReturnInternal(t, service, ticket, "update_ticket.failed")
}

func TestUpdate_DatabaseNetworkFailure(t *testing.T) {
	container, db, err := setupPostgresAndRunMigration()
	if err != nil {
		t.Logf("Error : %v", err)
		t.FailNow()
	}
	containers.CloseContainer(container)
	defer db.Close()

	service := NewTicketService(logging.New(logging.DebugLevel), db)

	ticket := &rpc.Ticket{
		Id:           1,
		TicketStatus: rpc.TicketStatus_RESOLVED,
	}
	updateShouldReturnInternal(t, service, ticket, "update_ticket.failed")
}

func TestUpdate(t *testing.T) {
	container, db, err := setupPostgresAndRunMigration()
	if err != nil {
		t.Logf("Error : %v", err)
		t.FailNow()
	}
	defer containers.CloseContainer(container)
	defer db.Close()

	service := NewTicketService(logging.New(logging.DebugLevel), db)

	ticket := &rpc.Ticket{
		Issuer:                "Jibit",
		Owner:                 "09203091992",
		Subject:               "Documentation",
		Content:               "Hello, i need some help about your technical documentation.",
		Metadata:              "{\"owner_ip\": \"185.186.187.188\"}",
		TicketImportanceLevel: rpc.TicketImportanceLevel_HIGH,
		TicketStatus:          rpc.TicketStatus_NEW,
	}

	if _, err := service.Create(context.Background(), ticket); err != nil {
		t.Logf("Error : %v", err)
		t.FailNow()
	}

	id := &rpc.Id{Id: 1}
	inserted, err := service.Read(context.Background(), id)
	if err != nil {
		t.Logf("Error : %v", err)
		t.FailNow()
	}

	ticket.Id = 1
	ticket.TicketStatus = rpc.TicketStatus_RESOLVED
	if _, err := service.Update(context.Background(), ticket); err != nil {
		t.Logf("Error : %v", err)
		t.FailNow()
	}

	updated, err := service.Read(context.Background(), id)
	if err != nil {
		t.Logf("Error : %v", err)
		t.FailNow()
	}

	if updated.TicketStatus != rpc.TicketStatus_RESOLVED {
		t.Logf("Actual: %v Expected: %v", updated.TicketStatus, rpc.TicketStatus_RESOLVED)
		t.FailNow()
	}

	insertedUpdatedAtTime, _ := time.Parse(time.RFC3339Nano, inserted.UpdatedAt)
	updatedUpdatedAtTime, _ := time.Parse(time.RFC3339Nano, updated.UpdatedAt)
	if !updatedUpdatedAtTime.After(insertedUpdatedAtTime) {
		t.Logf("Updated at column not updated properly.")
		t.FailNow()
	}
}

func createShouldReturnInvalidArgument(t *testing.T, service *TicketService, ticket *rpc.Ticket, message string) {
	_, err := service.Create(context.Background(), ticket)
	if err == nil {
		t.Logf("Expected error here!")
		t.FailNow()
	}

	status, ok := status.FromError(err)
	if !ok {
		t.Logf("The returned error is not compatible with gRPC error types.")
		t.FailNow()
	}

	if status.Code() != codes.InvalidArgument {
		t.Logf("Actual: %v Expected: %v", status.Code(), codes.InvalidArgument)
		t.FailNow()
	}

	if status.Message() != message {
		t.Logf("Actual: %v Expected: %v", status.Message(), message)
		t.FailNow()
	}
}

func createShouldReturnInternal(t *testing.T, service *TicketService, ticket *rpc.Ticket, message string) {
	_, err := service.Create(context.Background(), ticket)
	if err == nil {
		t.Logf("Expected error here!")
		t.FailNow()
	}

	status, ok := status.FromError(err)
	if !ok {
		t.Logf("The returned error is not compatible with gRPC error types.")
		t.FailNow()
	}

	if status.Code() != codes.Internal {
		t.Logf("Actual: %v Expected: %v", status.Code(), codes.InvalidArgument)
		t.FailNow()
	}

	if status.Message() != message {
		t.Logf("Actual: %v Expected: %v", status.Message(), message)
		t.FailNow()
	}
}

func readShouldReturnInvalidArgument(t *testing.T, service *TicketService, id *rpc.Id, message string) {
	_, err := service.Read(context.Background(), id)
	if err == nil {
		t.Logf("Expected error here!")
		t.FailNow()
	}

	status, ok := status.FromError(err)
	if !ok {
		t.Logf("The returned error is not compatible with gRPC error types.")
		t.FailNow()
	}

	if status.Code() != codes.InvalidArgument {
		t.Logf("Actual: %v Expected: %v", status.Code(), codes.InvalidArgument)
		t.FailNow()
	}

	if status.Message() != message {
		t.Logf("Actual: %v Expected: %v", status.Message(), message)
		t.FailNow()
	}
}

func readShouldReturnInternal(t *testing.T, service *TicketService, id *rpc.Id, message string) {
	_, err := service.Read(context.Background(), id)
	if err == nil {
		t.Logf("Expected error here!")
		t.FailNow()
	}

	status, ok := status.FromError(err)
	if !ok {
		t.Logf("The returned error is not compatible with gRPC error types.")
		t.FailNow()
	}

	if status.Code() != codes.Internal {
		t.Logf("Actual: %v Expected: %v", status.Code(), codes.Internal)
		t.FailNow()
	}

	if status.Message() != message {
		t.Logf("Actual: %v Expected: %v", status.Message(), message)
		t.FailNow()
	}
}

func readShouldReturnNotfound(t *testing.T, service *TicketService, id *rpc.Id, message string) {
	_, err := service.Read(context.Background(), id)
	if err == nil {
		t.Logf("Expected error here!")
		t.FailNow()
	}

	status, ok := status.FromError(err)
	if !ok {
		t.Logf("The returned error is not compatible with gRPC error types.")
		t.FailNow()
	}

	if status.Code() != codes.NotFound {
		t.Logf("Actual: %v Expected: %v", status.Code(), codes.NotFound)
		t.FailNow()
	}

	if status.Message() != message {
		t.Logf("Actual: %v Expected: %v", status.Message(), message)
		t.FailNow()
	}
}

func updateShouldReturnInvalidArgument(t *testing.T, service *TicketService, ticket *rpc.Ticket, message string) {
	_, err := service.Update(context.Background(), ticket)
	if err == nil {
		t.Logf("Expected error here!")
		t.FailNow()
	}

	status, ok := status.FromError(err)
	if !ok {
		t.Logf("The returned error is not compatible with gRPC error types.")
		t.FailNow()
	}

	if status.Code() != codes.InvalidArgument {
		t.Logf("Actual: %v Expected: %v", status.Code(), codes.InvalidArgument)
		t.FailNow()
	}

	if status.Message() != message {
		t.Logf("Actual: %v Expected: %v", status.Message(), message)
		t.FailNow()
	}
}

func updateShouldReturnInternal(t *testing.T, service *TicketService, ticket *rpc.Ticket, message string) {
	_, err := service.Update(context.Background(), ticket)
	if err == nil {
		t.Logf("Expected error here!")
		t.FailNow()
	}

	status, ok := status.FromError(err)
	if !ok {
		t.Logf("The returned error is not compatible with gRPC error types.")
		t.FailNow()
	}

	if status.Code() != codes.Internal {
		t.Logf("Actual: %v Expected: %v", status.Code(), codes.InvalidArgument)
		t.FailNow()
	}

	if status.Message() != message {
		t.Logf("Actual: %v Expected: %v", status.Message(), message)
		t.FailNow()
	}
}

func updateShouldReturnNotfound(t *testing.T, service *TicketService, ticket *rpc.Ticket, message string) {
	_, err := service.Update(context.Background(), ticket)
	if err == nil {
		t.Logf("Expected error here!")
		t.FailNow()
	}

	status, ok := status.FromError(err)
	if !ok {
		t.Logf("The returned error is not compatible with gRPC error types.")
		t.FailNow()
	}

	if status.Code() != codes.NotFound {
		t.Logf("Actual: %v Expected: %v", status.Code(), codes.NotFound)
		t.FailNow()
	}

	if status.Message() != message {
		t.Logf("Actual: %v Expected: %v", status.Message(), message)
		t.FailNow()
	}
}