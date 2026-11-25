package licenses

// ReadTrust lets the tests read a trusted-keys file of throwaway keys in place of the shipped one.
var ReadTrust = readTrust

// Shipped is the embedded trusted.json, so a test reads it as a release and as a dev build.
var Shipped = trusted

// TrustKey lets the tests ask which key a build takes for an id.
var TrustKey = Trust.key
