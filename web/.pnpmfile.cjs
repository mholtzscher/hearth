module.exports = {
  hooks: {
    readPackage(pkg) {
      // Storybook's docgen stack needs the JS compiler API; the app stays on TS 7.
      if (["storybook", "@storybook/react", "@storybook/react-vite", "@joshwooding/vite-plugin-react-docgen-typescript", "react-docgen-typescript"].includes(pkg.name)) {
        delete pkg.peerDependencies?.typescript;
        pkg.dependencies = { ...pkg.dependencies, typescript: "6.0.3" };
      }
      return pkg;
    },
  },
};
