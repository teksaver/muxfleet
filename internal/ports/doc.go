// Package ports holds the hexagonal interfaces between the application and its adapters (AD-1).
//
// Story 1 implements only ServerChannel, narrowed to what AD-5 allows. The other interfaces
// are drafts: the story that first implements a port adjusts it to the ADs that bind it
// before relying on it, and does not take its current shape as a settled contract.
package ports
