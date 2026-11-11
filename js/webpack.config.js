const path = require('path');
const TerserPlugin = require("terser-webpack-plugin");
const LicenseWebpackPlugin = require('license-webpack-plugin').LicenseWebpackPlugin;

var devtool;

if (process.env.DEV === '1') {
    devtool = 'inline-source-map';
} else {
    devtool = 'source-map';
}

module.exports = {
    entry: {
        "gotty": "./src/main.ts",
    },
    output: {
        path: path.resolve(__dirname, '../bindata/static/js/'),
        assetModuleFilename: 'fonts/[name][ext]',
    },
    devtool: devtool,
    resolve: {
        extensions: [".ts", ".tsx", ".js"],
    },
    plugins: [{apply(compiler) {
        compiler.hooks.thisCompilation.tap('TerminalFontLicenses', compilation => {
            compilation.hooks.processAssets.tap({name: 'TerminalFontLicenses', stage: compiler.webpack.Compilation.PROCESS_ASSETS_STAGE_ADDITIONAL}, () => {
                const fs = require('fs'), directory = path.join(__dirname, 'packages/terminal-core/font-licenses');
                for (const name of fs.readdirSync(directory)) compilation.emitAsset(`font-licenses/${name}`, new compiler.webpack.sources.RawSource(fs.readFileSync(path.join(directory, name))));
                compilation.emitAsset('font-licenses/manifest.json', new compiler.webpack.sources.RawSource(fs.readFileSync(path.join(__dirname, 'packages/terminal-core/font-manifest.json'))));
            });
        });
    }},
        new LicenseWebpackPlugin({
            licenseTextOverrides: {
                // Preact ships jsx-runtime as a subpackage without its own license file.
                "jsx-runtime": require("fs").readFileSync(require.resolve("preact/package.json").replace(/package\.json$/, "LICENSE"), "utf8")
            }
        })
    ],
    module: {
        rules: [
            { test: /\.woff2$/i, type: "asset/resource" },
            {
                test: /\.tsx?$/,
                loader: "esbuild-loader",
                exclude: /node_modules/,
                options: {
                    // Transpile only — no type checking here. Types are
                    // checked separately by `npm run typecheck` (tsgo), so
                    // the bundler no longer needs the TypeScript compiler
                    // API (which TypeScript 7 does not expose yet).
                    loader: "tsx",
                    target: "esnext",
                    jsx: "automatic",
                    jsxImportSource: "preact"
                }
            },
            {
                test: /\.css$/i,
                use: ["style-loader", "css-loader"],
            },
            {
                test: /\.scss$/i,
                use: ["style-loader", "css-loader", {
                    loader: "sass-loader",
                    options: {
                        sassOptions: {
                            loadPaths: ["node_modules/bootstrap/scss"],
                            silenceDeprecations: ["import"]
                        }
                    }
                }
                ],
            },
        ],
    },
    optimization: {
        // Keep scope hoisting off. webpack concatenates ESM modules, and
        // license-webpack-plugin cannot see ESM modules nested inside a
        // concatenated module: with it on, all six @xterm packages silently
        // disappeared from gotty.licenses.txt. It was never triggered before
        // because ts-loader emitted CommonJS (tsconfig "module": "commonJS");
        // esbuild-loader emits ESM. Costs ~6 KB in the bundle.
        concatenateModules: false,
        minimize: true,
        minimizer: [new TerserPlugin({
            terserOptions: {
                ecma: "2016"
            }
        })],
    },
};
