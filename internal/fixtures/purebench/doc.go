// Package purebench is a test fixture: a benchmark that reaches file I/O but
// carries the //gofresh:pure directive. It lives alone because the test-main root
// makes any benchmark's file I/O reach every test subject's closure in its package.
package purebench
