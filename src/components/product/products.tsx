"use client";
import { createCartStore } from "@/stores/cart";
import React from "react";
import ProductItem from "./product-item";

export default function Products() {
  const { products } = createCartStore();
  return (
    <div className="mt-l grid grid-cols-1 sm:grid-cols-2 lg:grid-cols-3 gap-4 overflow-y-auto">
      {products.map((product) => (
        <div key={product.id}>
          <ProductItem product={product} />
        </div>
      ))}
    </div>
  );
}
