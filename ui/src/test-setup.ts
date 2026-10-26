// Loaded by Vitest before each test file. The jest-dom matchers extend
// expect() with assertions like toBeInTheDocument / toHaveTextContent
// that aren't part of vitest's default Chai vocabulary.
import "@testing-library/jest-dom/vitest";

// jsdom lacks the pointer-capture and scroll APIs Radix Select calls when it opens.
if (typeof Element !== "undefined") {
  Element.prototype.hasPointerCapture ??= () => false;
  Element.prototype.releasePointerCapture ??= () => {};
  Element.prototype.scrollIntoView ??= () => {};
}
