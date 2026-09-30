/** @type {import('next').NextConfig} */
const nextConfig = {
  trailingSlash: true,
  output: "export",
  experimental: {
    esmExternals: false,
  },
  env: {
    API_URL: process.env.API_URL,
  },
  images: {
    unoptimized: true,
  },
  transpilePackages: [
    "antd",
    "@ant-design",
    "rc-util",
    "rc-pagination",
    "rc-picker",
    "rc-notification",
    "rc-tooltip",
    "rc-tree",
    "rc-table",
  ],
};

export default nextConfig;
