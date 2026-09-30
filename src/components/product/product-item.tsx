"use client";
import React, { useCallback } from "react";
import ImageContainer from "../image";
import { createCartStore, IProduct } from "@/stores/cart";
import QtyCounter from "./counter";

interface IProductItem {
  product: IProduct;
}

export default function ProductItem(props: IProductItem) {
  const { product } = props;
  const { addToCart, carts } = createCartStore();

  const isSelected = useCallback(() => {
    return carts[product.id] ? true : false;
  }, [carts, product]);

  return (
    <div className="w-full">
      <div
        style={{
          backgroundImage: `url(${product.image})`,
        }}
        className={`w-[240px] h-[250px] relative bg-center bg-cover rounded-lg
          ${isSelected() && "border-primary border-[2px] shadow-md"}
        `}
      >
        {/* If product is selected, show quantity controls */}
        <div
          className={`absolute bottom-[-17%] left-[50%] transform -translate-x-[50%] -translate-y-[65%] flex justify-center items-center py-s ${
            isSelected() ? "bg-primary" : "bg-white"
          } border border-grayBorder min-w-[70%] rounded-lg u`}
        >
          {isSelected() ? (
            <QtyCounter product={product} />
          ) : (
            // If not selected, show "Add to Cart" button
            <div
              className="flex justify-center items-center min-w-[150px] rounded-lg cursor-pointer"
              onClick={() => addToCart(product)}
            >
              <ImageContainer src="/assets/cart.png" alt={product.name} />
              <p className="txt ml-s">Add to Cart</p>
            </div>
          )}
        </div>
      </div>

      {/* Product Details */}
      <p className="label mt-l">{product.name}</p>
      <p className="title mt-xs">{product.desc}</p>
      <p className="txt text-[#bb7865] mt-xs">${product.price.toFixed(2)}</p>
    </div>
  );
}
