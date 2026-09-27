// Package gogogo creates projects from tar-based templates.
//
// Templates may use {{key}} and __key__ placeholders in text files.
// Binary files are copied byte-for-byte without placeholder substitution.
//
// Create consumes a caller-provided tar stream, optionally gzip-compressed, and
// strips one leading path component from each archive entry. It builds the project
// in a sibling staging directory before publishing it by rename. The caller owns
// the input reader and is responsible for closing it and arranging cancellation
// of blocking reads, such as reads from a network connection.
//
// Extraction rejects escaping paths, duplicate entries, unsupported entry types,
// and traversal through archive-created symlink parents. These checks are not a
// sandbox for generated content: templates and replacement values should be
// reviewed before building or executing the resulting project. Callers handling
// untrusted archives should also enforce their own input and resource limits.
//
// Repository helpers normalize template references and construct GitHub archive
// URLs; they do not download archives or verify repository access.
package gogogo
