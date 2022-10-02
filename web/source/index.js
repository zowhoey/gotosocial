/*
	GoToSocial
	Copyright (C) 2021-2022 GoToSocial Authors admin@gotosocial.org

	This program is free software: you can redistribute it and/or modify
	it under the terms of the GNU Affero General Public License as published by
	the Free Software Foundation, either version 3 of the License, or
	(at your option) any later version.

	This program is distributed in the hope that it will be useful,
	but WITHOUT ANY WARRANTY; without even the implied warranty of
	MERCHANTABILITY or FITNESS FOR A PARTICULAR PURPOSE.  See the
	GNU Affero General Public License for more details.

	You should have received a copy of the GNU Affero General Public License
	along with this program.  If not, see <http://www.gnu.org/licenses/>.
*/

"use strict";

/*
	Bundle the frontend panels for admin and user settings
*/

/*
 TODO: refactor dev-server to act as developer-facing webserver,
 proxying other requests to testrig instance. That way actual livereload works
*/

const Promise = require("bluebird");
const path = require('path');
const browserify = require("browserify");
const babelify = require('babelify');
const fsSync = require("fs");
const fs = require("fs").promises;
const chalk = require("chalk");

const debugMode = process.env.NODE_ENV == "development";

function out(name = "") {
	return path.join(__dirname, "../assets/dist/", name);
}

if (!fsSync.existsSync(out())){
	fsSync.mkdirSync(out(), { recursive: true });
}

module.exports = {out};

const splitCSS = require("./lib/split-css.js");

let cssFiles = fsSync.readdirSync(path.join(__dirname, "./css")).map((file) => {
	return path.join(__dirname, "./css", file);
});

const bundles = [
	{
		outputFile: "frontend.js",
		entryFiles: ["./frontend/index.js"],
		babelOptions: {
			global: true,
			exclude: /node_modules\/(?!photoswipe-dynamic-caption-plugin)/,
		}
	},
	{
		outputFile: "react-bundle.js",
		factors: {
			"./settings/index.js": "settings.js",
			"./swagger/index.js": "swagger.js",
		}
	},
	{
		outputFile: "_delete", // not needed, we only care for the css that's already split-out by css-extract
		entryFiles: cssFiles,
	}
];

const postcssPlugins = [
	"postcss-import",
	"postcss-nested",
	"autoprefixer",
	"postcss-custom-prop-vars",
	"postcss-color-mod-function"
].map((plugin) => require(plugin)());

function browserifyConfig({transforms = [], plugins = [], babelOptions = {}}) {
	if (!debugMode) {
		transforms.push([
			require("uglifyify"), {
				global: true,
				exts: ".js"
			}
		]);
	}

	return {
		transform: [
			[
				babelify.configure({
					presets: [
						[
							require.resolve("@babel/preset-env"),
							{
								modules: "cjs"
							}
						],
						require.resolve("@babel/preset-react")
					]
				}),
				babelOptions
			],
			...transforms
		],
		plugin: [
			[require("icssify"), {
				parser: require("postcss-scss"),
				before: postcssPlugins,
				mode: 'global'
			}],
			[require("css-extract"), { out: splitCSS }],
			...plugins
		],
		extensions: [".js", ".jsx", ".css"],
		fullPaths: debugMode,
		debug: debugMode
	};
}

bundles.forEach((bundle) => {
	let transforms, plugins, entryFiles;
	let { outputFile, babelOptions} = bundle;

	if (bundle.factors != undefined) {
		let factorBundle = [require("factor-bundle"), {
			outputs: Object.values(bundle.factors).map((file) => {
				return out(file);
			})
		}];

		plugins = [factorBundle];

		entryFiles = Object.keys(bundle.factors);
	} else {
		entryFiles = bundle.entryFiles;
	}

	let config = browserifyConfig({transforms, plugins, babelOptions, entryFiles, outputFile});

	Promise.try(() => {
		return browserify(entryFiles, config);
	}).then((bundler) => {
		Promise.promisifyAll(bundler);
		return bundler.bundleAsync();
	}).then((bundle) => {
		if (outputFile != "_delete") {
			console.log(chalk.magenta("JS: writing to", outputFile));
			return fs.writeFile(out(`_${outputFile}`), bundle);
		}
	}).catch((e) => {
		console.log(chalk.red("Fatal error in bundler:"), bundle.bundle);
		console.log(e.message);
		console.log(e.stack);
		console.log();
	});
});